use clickhouse::Client as ClickhouseClient;
use futures_util::StreamExt;
use tokio::sync::watch;

pub mod storage_usage_proto {
    include!(concat!(env!("OUT_DIR"), "/storage_usage.rs"));
}

#[derive(clickhouse::Row, serde::Serialize)]
struct ClickhouseBucketSizeRow {
    #[serde(with = "clickhouse::serde::time::datetime64::millis")]
    timestamp: time::OffsetDateTime,
    bucket_name: String,
    owner_id: String,
    owner_type: String,
    used_bytes: u64,
}

pub async fn run_size_syncer(
    nats_url: String,
    ch_client: ClickhouseClient,
    mut shutdown_rx: watch::Receiver<bool>,
) {
    println!("Storage Size Syncer Service: Khởi chạy subscriber NATS.");

    let nats_client = match async_nats::connect(&nats_url).await {
        Ok(c) => c,
        Err(e) => {
            eprintln!(
                "Storage Size Syncer Service: Lỗi kết nối NATS ({}): {}",
                nats_url, e
            );
            return;
        }
    };

    let mut subscriber = match nats_client
        .queue_subscribe(
            "billing.storage.bucket_used_bytes_update".to_string(),
            "billing_storage_usage_group".to_string(),
        )
        .await
    {
        Ok(s) => s,
        Err(e) => {
            eprintln!("Storage Size Syncer Service: Lỗi subscribe NATS: {}", e);
            return;
        }
    };

    println!(
        "Storage Size Syncer Service: Đăng ký thành công queue group 'billing_storage_usage_group'."
    );

    loop {
        tokio::select! {
            msg_opt = subscriber.next() => {
                if let Some(msg) = msg_opt {
                    match <storage_usage_proto::BucketUsedBytesUpdate as prost::Message>::decode(msg.payload) {
                        Ok(req) => {
                            let ts = time::OffsetDateTime::from_unix_timestamp_nanos(req.timestamp as i128 * 1_000_000)
                                .unwrap_or_else(|_| time::OffsetDateTime::now_utc());

                            let row = ClickhouseBucketSizeRow {
                                timestamp: ts,
                                bucket_name: req.bucket_name.clone(),
                                owner_id: req.owner_id.clone(),
                                owner_type: req.owner_type.clone(),
                                used_bytes: req.used_bytes,
                            };

                            let mut inserter = match ch_client.insert("bucket_size_history") {
                                Ok(i) => i,
                                Err(e) => {
                                    eprintln!("Storage Size Syncer: Lỗi tạo inserter ClickHouse: {}", e);
                                    continue;
                                }
                            };

                            if let Err(e) = inserter.write(&row).await {
                                eprintln!("Storage Size Syncer: Lỗi write ClickHouse: {}", e);
                                continue;
                            }

                            if let Err(e) = inserter.end().await {
                                eprintln!("Storage Size Syncer: Lỗi commit ClickHouse: {}", e);
                                continue;
                            }

                            println!(
                                "Storage Size Syncer: Đã ghi nhận dung lượng bucket '{}' ({} bytes) vào ClickHouse",
                                req.bucket_name, req.used_bytes
                            );
                        }
                        Err(e) => {
                            eprintln!("Storage Size Syncer: Lỗi giải mã protobuf: {}", e);
                        }
                    }
                }
            }
            _ = shutdown_rx.changed() => {
                if *shutdown_rx.borrow() {
                    println!("Storage Size Syncer Service: Nhận tín hiệu shutdown. Đang thoát...");
                    break;
                }
            }
        }
    }
}
