use crate::config::Config;
use sqlx::PgPool;
use clickhouse::Client as ClickhouseClient;
use redis::aio::MultiplexedConnection;
use tokio::sync::watch;

// [COMMENT]: Hàm đăng ký và điều phối các background services
pub async fn run_services(
    config: Config,
    pg_pool: PgPool,
    ch_client: ClickhouseClient,
    redis_conn: MultiplexedConnection,
    mut shutdown_rx: watch::Receiver<bool>,
) {
    println!("Đang đăng ký và khởi chạy các background services...");

    // [COMMENT]: Spawn job xử lý tính cước cho dịch vụ Storage
    let storage_billing_handle = tokio::spawn(
        crate::service::storage::billing::run_billing_job(
            config.clone(),
            pg_pool.clone(),
            ch_client.clone(),
            redis_conn.clone(),
            shutdown_rx.clone(),
        )
    );

    // [COMMENT]: Spawn job đồng bộ dung lượng lưu trữ từ NATS
    let nats_url = config.nats_url.clone();
    let size_syncer_ch_client = ch_client.clone();
    let size_syncer_shutdown_rx = shutdown_rx.clone();
    let storage_size_syncer_handle = tokio::spawn(
        crate::service::storage::size_syncer::run_size_syncer(
            nats_url,
            size_syncer_ch_client,
            size_syncer_shutdown_rx,
        )
    );

    // [COMMENT]: Theo dõi và quản lý vòng đời của các services dựa trên watch channel shutdown
    tokio::select! {
        res = storage_billing_handle => {
            match res {
                Ok(_) => println!("Storage Billing Service đã dừng."),
                Err(e) => eprintln!("Lỗi nghiêm trọng tại Storage Billing Service: {:?}", e),
            }
        }
        res = storage_size_syncer_handle => {
            match res {
                Ok(_) => println!("Storage Size Syncer Service đã dừng."),
                Err(e) => eprintln!("Lỗi nghiêm trọng tại Storage Size Syncer Service: {:?}", e),
            }
        }
        _ = shutdown_rx.changed() => {
            println!("Đã nhận tín hiệu dừng từ main engine. Đang yêu cầu các service tắt gracefully...");
        }
    }
}
