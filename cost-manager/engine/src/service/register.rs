use crate::config::Config;
use clickhouse::Client as ClickhouseClient;
use redis::aio::MultiplexedConnection;
use sqlx::PgPool;
use std::sync::Arc;
use tokio::sync::watch;

use crate::engine::PricingRuntime;

// [COMMENT]: Hàm đăng ký và điều phối các background services
pub async fn run_services(
    config: Config,
    pg_pool: PgPool,
    ch_client: ClickhouseClient,
    redis_conn: MultiplexedConnection,
    pricing_runtime: Arc<PricingRuntime>,
    mut shutdown_rx: watch::Receiver<bool>,
) {
    println!("Đang đăng ký và khởi chạy các background services...");

    // [COMMENT]: Spawn job xử lý tính cước cho dịch vụ Storage
    let storage_billing_handle = tokio::spawn(crate::service::storage::egress_billing::run_storage_egress_billing(
        config.clone(),
        pg_pool.clone(),
        ch_client.clone(),
        redis_conn.clone(),
        pricing_runtime.clone(),
        shutdown_rx.clone(),
    ));

    // [COMMENT]: Mỗi replica subscribe broadcast để L1 luôn warm và failover không phải đợi reload lạnh.
    let pricing_listener_handle = tokio::spawn(crate::engine::run_pricing_listener(
        config.nats_url.clone(),
        pricing_runtime,
        shutdown_rx.clone(),
    ));

    // [COMMENT]: Spawn job đồng bộ dung lượng lưu trữ từ NATS
    let nats_url = config.nats_url.clone();
    let size_syncer_ch_client = ch_client.clone();
    let size_syncer_shutdown_rx = shutdown_rx.clone();
    let storage_size_syncer_handle =
        tokio::spawn(crate::service::storage::size_syncer::run_size_syncer(
            nats_url,
            size_syncer_ch_client,
            size_syncer_shutdown_rx,
        ));

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
        res = pricing_listener_handle => {
            match res {
                Ok(_) => println!("Pricing Listener đã dừng."),
                Err(e) => eprintln!("Lỗi nghiêm trọng tại Pricing Listener: {:?}", e),
            }
        }
        _ = shutdown_rx.changed() => {
            println!("Đã nhận tín hiệu dừng từ main engine. Đang yêu cầu các service tắt gracefully...");
        }
    }
}
