use crate::config::Config;
use crate::observability::logger::Logger;
use tokio_postgres::NoTls;
use std::time::Duration;

/// Định nghĩa lỗi chí mạng không thể tự phục hồi (ví dụ: thiếu bảng trong DB, sai cấu hình, thiếu quyền)
#[derive(Debug)]
pub struct UnrecoverableError(pub String);

impl std::fmt::Display for UnrecoverableError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "Lỗi CDC chí mạng: {}", self.0)
    }
}

impl std::error::Error for UnrecoverableError {}

/// Hàm kiểm tra xem lỗi phát sinh có thuộc nhóm không thể tự phục hồi hay không
fn is_unrecoverable(err: &(dyn std::error::Error + 'static)) -> bool {
    // 1. Nếu là lỗi UnrecoverableError tự định nghĩa
    if err.is::<UnrecoverableError>() {
        return true;
    }

    // 2. Nếu là lỗi trả về từ PostgreSQL
    if let Some(pg_err) = err.downcast_ref::<tokio_postgres::Error>() {
        if let Some(db_err) = pg_err.as_db_error() {
            let code = db_err.code().code();
            // Phân loại các mã lỗi Postgres chí mạng:
            // 42P01: undefined_table (Không tồn tại bảng Outbox vật lý)
            // 42501: insufficient_privilege (Thiếu quyền REPLICATION/Superuser)
            // 42704: undefined_object (Thiếu plugin hoặc replication slot không hợp lệ)
            return code == "42P01" || code == "42501" || code == "42704";
        }
    }
    false
}

/// Khởi tạo hạ tầng Logical Replication (Publication và Replication Slot) cho PostgreSQL.
/// Hàm này được thiết kế để chỉ chạy một lần duy nhất lúc khởi chạy ứng dụng (main.rs).
/// Có cơ chế tự động thử lại kết nối (reconnect) theo cấp số nhân và dừng ngay lập tức (fail-fast) khi gặp lỗi cấu hình/bảng chí mạng.
pub async fn setup_replication_infrastructure(config: &Config) -> Result<(), Box<dyn std::error::Error>> {
    Logger::sys_info(
        "cdc.setup",
        "CdcSetup: Bắt đầu kiểm tra và khởi tạo hạ tầng Logical Replication...",
    );

    let mut retries = 0;
    let max_retries = config.max_setup_retries;

    loop {
        // Thực thi kiểm tra kết nối và cấu hình publication/slot
        match try_setup(config).await {
            Ok(_) => {
                Logger::sys_info(
                    "cdc.setup",
                    "CdcSetup: Toàn bộ hạ tầng Logical Replication đã được thiết lập thành công.",
                );
                return Ok(());
            }
            Err(e) => {
                retries += 1;

                // Kiểm tra điều kiện dừng ngay (Fail-Fast)
                if is_unrecoverable(e.as_ref()) || retries >= max_retries {
                    Logger::sys_error(
                        "cdc.setup",
                        "CdcSetup: Thất bại chí mạng trong quá trình setup hạ tầng. Dừng ứng dụng.",
                        &e.to_string(),
                    );
                    return Err(e);
                }

                // Tính toán Exponential Backoff: đợi thời gian 2^retries giây (giới hạn tối đa 30s)
                let backoff_secs = (2u64.pow(retries)).min(30);
                Logger::sys_warn(
                    "cdc.setup",
                    &format!(
                        "CdcSetup: Gặp sự cố kết nối, sẽ thử lại lần {}/{} sau {} giây...",
                        retries, max_retries, backoff_secs
                    ),
                    &e.to_string(),
                );
                tokio::time::sleep(Duration::from_secs(backoff_secs)).await;
            }
        }
    }
}

/// Thực hiện kết nối PostgreSQL và thiết lập Publication + Replication Slot cho danh sách các bảng CDC
async fn try_setup(config: &Config) -> Result<(), Box<dyn std::error::Error>> {
    // 1. Thực hiện kết nối PostgreSQL thông thường để cấu hình hạ tầng
    let (client, connection) = tokio_postgres::connect(&config.database_url, NoTls).await?;
    
    // Spawn kết nối chạy ngầm để trao đổi các thông điệp giao thức của tokio-postgres
    tokio::spawn(async move {
        if let Err(e) = connection.await {
            Logger::sys_error(
                "cdc.setup.postgres",
                "CdcSetup: Gặp lỗi trong luồng kết nối chạy ngầm của PostgreSQL",
                &e.to_string(),
            );
        }
    });

    // Thiết lập search path mặc định
    client.execute("SET search_path TO mail, public", &[]).await?;

    // 2. Pre-check và khởi tạo Publication cho tất cả các bảng trong danh sách cdc_sources
    for source in &config.cdc_sources {
        // Tách schema và table (ví dụ: mail.mail_outbox_records)
        let parts: Vec<&str> = source.split('.').collect();
        let (schema, table) = if parts.len() == 2 {
            (parts[0], parts[1])
        } else {
            ("public", parts[0])
        };

        // Bước A: Kiểm tra xem bảng có tồn tại vật lý trong database hay không (Pre-check Fail-Fast)
        let table_exists = client.query(
            "SELECT 1 FROM information_schema.tables WHERE table_schema = $1 AND table_name = $2",
            &[&schema, &table],
        ).await?;

        if table_exists.is_empty() {
            return Err(Box::new(UnrecoverableError(format!(
                "Bảng Outbox '{}.{}' cấu hình trong CDC_SOURCES không tồn tại trong cơ sở dữ liệu",
                schema, table
            ))));
        }

        // Bước B: Đảm bảo Publication chung tồn tại
        let pub_check = client.query(
            "SELECT 1 FROM pg_publication WHERE pubname = $1",
            &[&config.publication_name],
        ).await?;

        if pub_check.is_empty() {
            Logger::sys_info(
                "cdc.setup",
                &format!("CdcSetup: Tạo mới publication '{}' cho cụm outbox...", config.publication_name),
            );
            let create_pub_sql = format!(
                "CREATE PUBLICATION {}",
                config.publication_name
            );
            if let Err(err) = client.execute(&create_pub_sql, &[]).await {
                let err_str = err.to_string();
                // Bắt lỗi trùng lặp đối tượng phòng trường hợp tranh chấp HA khi nhiều instance chạy song song
                if !err_str.contains("already exists") && !err_str.contains("42710") {
                    return Err(err.into());
                }
            }
        }

        // Bước C: Đảm bảo bảng này đã được liên kết vào Publication
        let table_check = client.query(
            "SELECT 1 FROM pg_publication_tables WHERE pubname = $1 AND schemaname = $2 AND tablename = $3",
            &[&config.publication_name, &schema, &table],
        ).await?;

        if table_check.is_empty() {
            Logger::sys_info(
                "cdc.setup",
                &format!("CdcSetup: Thêm bảng '{}.{}' vào publication '{}'...", schema, table, config.publication_name),
            );
            let alter_pub_sql = format!(
                "ALTER PUBLICATION {} ADD TABLE {}.{}",
                config.publication_name, schema, table
            );
            if let Err(err) = client.execute(&alter_pub_sql, &[]).await {
                let err_str = err.to_string();
                // Bỏ qua lỗi nếu bảng đã được replica khác thêm vào trước đó
                if !err_str.contains("already is member") && !err_str.contains("duplicate") {
                    return Err(err.into());
                }
            }
        }
    }

    // 3. Đảm bảo Replication Slot tồn tại với plugin pgoutput
    let slot_check = client.query(
        "SELECT 1 FROM pg_replication_slots WHERE slot_name = $1 AND plugin = 'pgoutput'",
        &[&config.slot_name],
    ).await?;

    if slot_check.is_empty() {
        Logger::sys_info(
            "cdc.setup",
            &format!("CdcSetup: Tạo mới replication slot '{}' với plugin pgoutput...", config.slot_name),
        );
        let create_slot_sql = format!(
            "SELECT lsn FROM pg_create_logical_replication_slot('{}', 'pgoutput')",
            config.slot_name
        );
        if let Err(err) = client.query(&create_slot_sql, &[]).await {
            let err_str = err.to_string();
            // Bỏ qua lỗi trùng lặp đối tượng do tranh chấp HA
            if !err_str.contains("already exists") && !err_str.contains("42710") {
                return Err(err.into());
            }
        }
    }

    Ok(())
}
