use std::time::Duration;
use tokio::time::sleep;
use sqlx::postgres::PgPool;
use sqlx::Postgres;
use redis::AsyncCommands;
use clickhouse::Client as ClickhouseClient;
use serde::Deserialize;
use uuid::Uuid;
use chrono::{DateTime, Utc};
use bigdecimal::{BigDecimal, ToPrimitive, FromPrimitive};
use tokio::sync::watch;

use crate::config::Config;

// [COMMENT]: Định nghĩa Namespace UUID cố định để sinh UUID v5 cho tính chất Idempotency
const S3_BILLING_NAMESPACE: Uuid = Uuid::from_u128(0x5a18a5cb33a647d6be96cb57f70b13cf);

// [COMMENT]: ClickhouseMeteringRow đại diện cho dữ liệu băng thông gộp theo giờ lấy từ ClickHouse
#[derive(Debug, Deserialize, clickhouse::Row)]
pub struct ClickhouseMeteringRow {
    pub hour: DateTime<Utc>,
    pub access_key: String,
    pub bucket_name: String,
    pub total_upload_bytes: u64,
    pub total_download_bytes: u64,
    pub request_count: u64,
}

// [COMMENT]: PriceConfig lưu cấu hình giá từ Postgres
#[derive(sqlx::FromRow)]
struct PriceConfig {
    unit_price: BigDecimal,
}

// [COMMENT]: Chạy core engine loop tính cước cho dịch vụ Storage
pub async fn run_billing_job(
    config: Config,
    pg_pool: PgPool,
    ch_client: ClickhouseClient,
    mut redis_conn: redis::aio::MultiplexedConnection,
    mut shutdown_rx: watch::Receiver<bool>,
) {
    println!("Storage Billing Service: Đã khởi chạy vòng lặp tính cước.");

    loop {
        // [COMMENT]: Kiểm tra tín hiệu shutdown trước khi vào chu kỳ mới
        if *shutdown_rx.borrow() {
            println!("Storage Billing Service: Nhận tín hiệu shutdown. Đang thoát...");
            break;
        }

        println!("Storage Billing Service: Bắt đầu chu kỳ quét cước và kiểm tra ví...");

        // [COMMENT]: Lấy Distributed Lock qua Redis để tránh các replica HA chạy trùng
        let lock_key = "storage:billing:lock";
        let lock_res: Option<String> = redis::cmd("SET")
            .arg(lock_key)
            .arg("locked")
            .arg("NX")
            .arg("PX")
            .arg(config.lock_ttl_secs * 1000)
            .query_async(&mut redis_conn)
            .await
            .ok()
            .flatten();

        if lock_res.is_none() {
            println!("Storage Billing Service: Không thể lấy Distributed Lock. Có thể node khác đang xử lý. Bỏ qua chu kỳ này.");
            // Chờ chu kỳ tiếp theo hoặc tín hiệu shutdown
            tokio::select! {
                _ = sleep(config.scan_interval) => {}
                _ = shutdown_rx.changed() => {
                    println!("Storage Billing Service: Nhận tín hiệu shutdown khi đang chờ.");
                    break;
                }
            }
            continue;
        }

        println!("Storage Billing Service: Đã lấy thành công Distributed Lock. Tiến hành quét cước...");

        // [COMMENT]: Lấy mốc thời gian checkpoint từ Redis để quét
        let last_processed_str: Option<String> = redis_conn.get("storage:billing:last_processed_time").await.ok().flatten();
        let last_processed = match last_processed_str {
            Some(t) => DateTime::parse_from_rfc3339(&t)
                .map(|dt| dt.with_timezone(&Utc))
                .unwrap_or_else(|_| Utc::now() - Duration::from_secs(3600)),
            None => Utc::now() - Duration::from_secs(3600), // Mặc định quét từ 1 giờ trước
        };

        println!("Storage Billing Service: Quét dữ liệu ClickHouse từ thời điểm: {:?}", last_processed);

        // [COMMENT]: A. Truy vấn dữ liệu băng thông mới từ ClickHouse
        let query = format!(
            "SELECT hour, access_key, bucket_name, total_upload_bytes, total_download_bytes, request_count \
             FROM hourly_metering_agg \
             WHERE hour > toDateTime('{}') \
             ORDER BY hour ASC",
            last_processed.format("%Y-%m-%d %H:%M:%S")
        );

        let mut max_hour = last_processed;
        let mut cursor = match ch_client.query(&query).fetch::<ClickhouseMeteringRow>() {
            Ok(c) => c,
            Err(e) => {
                eprintln!("Storage Billing Service: Lỗi fetch data từ ClickHouse: {:?}", e);
                // Giải phóng lock
                let _: Result<(), redis::RedisError> = redis_conn.del(lock_key).await;
                
                tokio::select! {
                    _ = sleep(Duration::from_secs(10)) => {}
                    _ = shutdown_rx.changed() => break,
                }
                continue;
            }
        };

        // [COMMENT]: B. Lấy giá của dịch vụ Egress Traffic trong Postgres (đơn giá mặc định / GB)
        let price_opt = sqlx::query_as::<Postgres, PriceConfig>(
            "SELECT unit_price FROM billing.prices WHERE service_type = 'TRAFFIC_EGRESS_GB' LIMIT 1"
        )
        .fetch_optional(&pg_pool)
        .await
        .ok()
        .flatten();

        let egress_price = match price_opt {
            Some(p) => p.unit_price.to_f64().unwrap_or(1000.0),
            None => 1000.0, // Đơn giá dự phòng nếu DB chưa cấu hình
        };

        let mut processed_records = 0;

        // [COMMENT]: C. Duyệt qua từng dòng log băng thông để xử lý trừ cước
        while let Ok(Some(row)) = cursor.next().await {
            // [COMMENT]: Hỗ trợ graceful shutdown ngay lập tức khi đang xử lý hàng loạt record
            if *shutdown_rx.borrow() {
                println!("Storage Billing Service: Nhận tín hiệu shutdown giữa chừng. Dừng xử lý các record tiếp theo để tắt an toàn.");
                break;
            }

            println!(
                "Storage Billing Service: Xử lý cước cho Key: {}, Bucket: {}, Egress: {} bytes, Ingress: {} bytes, Requests: {}",
                row.access_key, row.bucket_name, row.total_download_bytes, row.total_upload_bytes, row.request_count
            );

            if row.hour > max_hour {
                max_hour = row.hour;
            }

            // [COMMENT]: Tra cứu owner_id từ access_key trong DB
            let owner_opt: Option<(Uuid, String)> = sqlx::query_as::<Postgres, (Uuid, String)>(
                "SELECT owner_id, owner_type FROM billing.credential_owners WHERE access_key = $1"
            )
            .bind(&row.access_key)
            .fetch_optional(&pg_pool)
            .await
            .unwrap_or(None);

            let (owner_id, owner_type) = match owner_opt {
                Some(o) => o,
                None => {
                    eprintln!("Storage Billing Service: Warning: Không tìm thấy Owner cho access_key '{}'. Bỏ qua cước.", row.access_key);
                    continue;
                }
            };

            // [COMMENT]: Tính tiền cước: (download bytes / 1024^3) * egress_price
            let egress_gb = (row.total_download_bytes as f64) / (1024.0 * 1024.0 * 1024.0);
            let cost = egress_gb * egress_price;

            if cost <= 0.0001 {
                continue; // Cước quá nhỏ không đáng kể, bỏ qua
            }

            // [COMMENT]: Sinh UUID v5 duy nhất cho transaction dựa trên các trường định danh để đảm bảo tính Idempotency
            // Giúp ngăn chặn việc trừ cước trùng lặp (double-charging) trong trường hợp crash và xử lý lại
            let identifier = format!("{}:{}:{}", row.access_key, row.bucket_name, row.hour.to_rfc3339());
            let tx_id = Uuid::new_v5(&S3_BILLING_NAMESPACE, identifier.as_bytes());

            // [COMMENT]: Chạy transaction cập nhật số dư trong Postgres
            let mut tx = match pg_pool.begin().await {
                Ok(t) => t,
                Err(e) => {
                    eprintln!("Storage Billing Service: Lỗi khởi tạo Postgres Tx: {:?}", e);
                    continue;
                }
            };

            // [COMMENT]: Lock ví và lấy số dư hiện tại bằng SELECT ... FOR UPDATE để tránh race-condition với các transaction khác
            let wallet_opt: Option<(Uuid, BigDecimal, BigDecimal, String)> = sqlx::query_as::<Postgres, (Uuid, BigDecimal, BigDecimal, String)>(
                "SELECT id, balance, overdraft_limit, status FROM billing.wallets WHERE owner_id = $1 AND owner_type = $2 FOR UPDATE"
            )
            .bind(owner_id)
            .bind(&owner_type)
            .fetch_optional(&mut *tx)
            .await
            .unwrap_or(None);

            if let Some((wallet_id, balance, overdraft_limit, status)) = wallet_opt {
                let current_balance = balance.to_f64().unwrap_or(0.0);
                let limit = overdraft_limit.to_f64().unwrap_or(0.0);

                let new_balance = current_balance - cost;
                let mut new_status = status.clone();

                // [COMMENT]: Nếu số dư ví vượt quá hạn mức nợ -> Khóa tài khoản
                if new_balance + limit <= 0.0 && status == "ACTIVE" {
                    new_status = "SUSPENDED".to_string();
                    println!("Storage Billing Service: Tài khoản {} hết tiền! Đánh dấu khóa SUSPENDED.", owner_id);

                    // [COMMENT]: Đẩy key khóa lên Redis để các Gateway chặn thời gian thực
                    let block_key = format!("storage:blocked_keys:{}", row.access_key);
                    // Đặt TTL theo cấu hình (ví dụ: 30 ngày) để đảm bảo không bị tự động mở khóa sau 1 giờ
                    let _: Result<(), redis::RedisError> = redis_conn.set_ex(&block_key, "true", config.block_key_ttl_secs).await;
                }

                // [COMMENT]: Cập nhật ví tiền của khách hàng
                let update_wallet_res = sqlx::query(
                    "UPDATE billing.wallets SET balance = $1, status = $2, updated_at = CURRENT_TIMESTAMP WHERE id = $3"
                )
                .bind(BigDecimal::from_f64(new_balance).unwrap_or(BigDecimal::from(0)))
                .bind(&new_status)
                .bind(wallet_id)
                .execute(&mut *tx)
                .await;

                if let Err(e) = update_wallet_res {
                    eprintln!("Storage Billing Service: Lỗi cập nhật ví: {:?}", e);
                    let _ = tx.rollback().await;
                    continue;
                }

                // [COMMENT]: Ghi nhật ký giao dịch (Ledger) sử dụng ID idempotent (tx_id)
                let desc = format!("Trừ cước băng thông S3: {:.4} GB", egress_gb);
                let insert_ledger_res = sqlx::query(
                    "INSERT INTO billing.transactions (id, wallet_id, amount, tx_type, service_type, reference_id, description) \
                     VALUES ($1, $2, $3, $4, $5, $6, $7)"
                )
                .bind(tx_id)
                .bind(wallet_id)
                .bind(BigDecimal::from_f64(-cost).unwrap_or(BigDecimal::from(0)))
                .bind("USAGE_CHARGE")
                .bind("STORAGE")
                .bind("s3-egress")
                .bind(desc)
                .execute(&mut *tx)
                .await;

                match insert_ledger_res {
                    Ok(_) => {
                        // [COMMENT]: Giao dịch thành công
                        if let Err(e) = tx.commit().await {
                            eprintln!("Storage Billing Service: Lỗi commit transaction: {:?}", e);
                        } else {
                            println!("Storage Billing Service: Trừ tiền thành công cho owner {}: -{:.4} VND (Tx ID: {})", owner_id, cost, tx_id);
                            processed_records += 1;
                        }
                    }
                    Err(e) => {
                        // [COMMENT]: Kiểm tra lỗi trùng khóa chính Postgres (Unique Violation SQLSTATE 23505)
                        if let Some(db_err) = e.as_database_error() {
                            if db_err.code().as_deref() == Some("23505") {
                                // Giao dịch này đã được thực hiện rồi, rollback ví và bỏ qua an toàn (Idempotent)
                                println!("Storage Billing Service: Giao dịch {} đã tồn tại. Bỏ qua cước trùng lặp.", tx_id);
                                let _ = tx.rollback().await;
                                continue;
                            }
                        }
                        // Lỗi khác
                        eprintln!("Storage Billing Service: Lỗi insert transaction ledger: {:?}", e);
                        let _ = tx.rollback().await;
                    }
                }
            } else {
                let _ = tx.rollback().await;
            }
        }

        // [COMMENT]: D. Cập nhật checkpoint mới lên Redis sau khi xử lý thành công (hoặc dừng do shutdown)
        if max_hour > last_processed && processed_records > 0 {
            let _: Result<(), redis::RedisError> = redis_conn.set("storage:billing:last_processed_time", max_hour.to_rfc3339()).await;
            println!("Storage Billing Service: Đã cập nhật checkpoint lên thời điểm: {:?}", max_hour);
        }

        // [COMMENT]: Giải phóng Distributed Lock
        let _: Result<(), redis::RedisError> = redis_conn.del(lock_key).await;
        println!("Storage Billing Service: Đã giải phóng Distributed Lock.");

        // [COMMENT]: Chờ chu kỳ quét tiếp theo hoặc nhận tín hiệu shutdown sớm
        tokio::select! {
            _ = sleep(config.scan_interval) => {}
            _ = shutdown_rx.changed() => {
                println!("Storage Billing Service: Nhận tín hiệu shutdown khi đang chờ.");
                break;
            }
        }
    }
}
