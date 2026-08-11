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

    // [COMMENT]: Report-driven settlement is opt-in until the reconciliation
    // and cutover gates are approved. The default remains the legacy path so
    // a deployment can never debit through both workflows at once.
    let storage_billing_handle = if config.storage_report_settlement_enabled {
        tokio::spawn(
            crate::service::storage::usage_report_settlement::run_storage_usage_report_settlement(
                config.clone(),
                pg_pool.clone(),
                redis_conn.clone(),
                pricing_runtime.clone(),
                shutdown_rx.clone(),
            ),
        )
    } else {
        tokio::spawn(
            crate::service::storage::egress_billing::run_storage_egress_billing(
                config.clone(),
                pg_pool.clone(),
                ch_client.clone(),
                redis_conn.clone(),
                pricing_runtime.clone(),
                shutdown_rx.clone(),
            ),
        )
    };

    // [COMMENT]: Mỗi replica subscribe Shared Redis broadcast để L1 luôn warm và failover
    // không phải đợi reload lạnh; periodic DB reconcile vẫn là safety net.
    let pricing_listener_handle = tokio::spawn(crate::engine::run_pricing_listener(
        config.redis_url.clone(),
        pricing_runtime,
        shutdown_rx.clone(),
    ));

    // [COMMENT]: Theo dõi và quản lý vòng đời của các services dựa trên watch channel shutdown
    tokio::select! {
        res = storage_billing_handle => {
            match res {
                Ok(_) => println!("Storage Billing Service đã dừng."),
                Err(e) => eprintln!("Lỗi nghiêm trọng tại Storage Billing Service: {:?}", e),
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
