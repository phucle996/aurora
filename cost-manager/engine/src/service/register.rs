use crate::config::Config;
use redis::aio::MultiplexedConnection;
use sqlx::PgPool;
use std::sync::Arc;
use tokio::sync::watch;
use tokio::task::JoinSet;

use crate::engine::PricingRuntime;

// [COMMENT]: Hàm đăng ký và điều phối các background services
pub async fn run_services(
    config: Config,
    pg_pool: PgPool,
    redis_conn: MultiplexedConnection,
    pricing_runtime: Arc<PricingRuntime>,
    mut shutdown_rx: watch::Receiver<bool>,
) -> Result<(), String> {
    println!("Đang đăng ký và khởi chạy các background services...");

    // Storage usage reports and Hypervisor allocation/network evidence are
    // the only charge-producing module adapters. The old Central ClickHouse
    // polling worker is intentionally removed before production.
    let mut services = JoinSet::new();
    let storage_config = config.clone();
    let storage_pool = pg_pool.clone();
    let storage_redis = redis_conn.clone();
    let storage_pricing = pricing_runtime.clone();
    let storage_shutdown = shutdown_rx.clone();
    services.spawn(async move {
        crate::service::storage::usage_report_settlement::run_storage_usage_report_settlement(
            storage_config,
            storage_pool,
            storage_redis,
            storage_pricing,
            storage_shutdown,
        )
        .await;
        "storage usage settlement"
    });

    let hypervisor_lifecycle_pool = pg_pool.clone();
    let hypervisor_lifecycle_redis = redis_conn.clone();
    let hypervisor_lifecycle_shutdown = shutdown_rx.clone();
    services.spawn(async move {
        crate::service::hypervisor::allocation_lifecycle::run_hypervisor_allocation_lifecycle(
            hypervisor_lifecycle_pool,
            hypervisor_lifecycle_redis,
            hypervisor_lifecycle_shutdown,
        )
        .await;
        "hypervisor allocation lifecycle"
    });

    let hypervisor_settlement_pool = pg_pool.clone();
    let hypervisor_settlement_pricing = pricing_runtime.clone();
    let hypervisor_settlement_shutdown = shutdown_rx.clone();
    services.spawn(async move {
        crate::service::hypervisor::hourly_allocation_settlement::run_hypervisor_hourly_allocation_settlement(
            hypervisor_settlement_pool,
            hypervisor_settlement_pricing,
            hypervisor_settlement_shutdown,
        )
        .await;
        "hypervisor hourly allocation settlement"
    });

    let hypervisor_network_pool = pg_pool.clone();
    let hypervisor_network_redis = redis_conn.clone();
    let hypervisor_network_pricing = pricing_runtime.clone();
    let hypervisor_network_shutdown = shutdown_rx.clone();
    services.spawn(async move {
        crate::service::hypervisor::network_usage_settlement::run_hypervisor_network_usage_settlement(
            hypervisor_network_pool,
            hypervisor_network_redis,
            hypervisor_network_pricing,
            hypervisor_network_shutdown,
        )
        .await;
        "hypervisor network usage settlement"
    });

    let mail_usage_pool = pg_pool.clone();
    let mail_usage_redis = redis_conn.clone();
    let mail_usage_pricing = pricing_runtime.clone();
    let mail_usage_shutdown = shutdown_rx.clone();
    services.spawn(async move {
        crate::service::mail::accepted_usage_settlement::run_mail_accepted_usage_settlement(
            mail_usage_pool,
            mail_usage_redis,
            mail_usage_pricing,
            mail_usage_shutdown,
        )
        .await;
        "mail accepted usage settlement"
    });

    let activation_pool = pg_pool.clone();
    let activation_pricing = pricing_runtime.clone();
    let activation_shutdown = shutdown_rx.clone();
    services.spawn(async move {
        crate::service::storage::pending_activation_reconcile::run_pending_activation_reconciliation(
            activation_pool,
            activation_pricing,
            activation_shutdown,
        )
        .await;
        "storage pending activation reconciliation"
    });

    // [COMMENT]: Mỗi replica subscribe Shared Redis broadcast để L1 luôn warm và failove
    // không phải đợi reload lạnh; periodic DB reconcile vẫn là safety net.
    let pricing_redis_url = config.redis_url.clone();
    let listener_pricing = pricing_runtime;
    let listener_shutdown = shutdown_rx.clone();
    services.spawn(async move {
        crate::engine::run_pricing_listener(pricing_redis_url, listener_pricing, listener_shutdown)
            .await;
        "pricing listener"
    });

    tokio::select! {
        biased;
        changed = shutdown_rx.changed() => {
            if changed.is_err() || *shutdown_rx.borrow() {
                println!("Đã nhận tín hiệu dừng từ main engine. Đang chờ các service tắt gracefully...");
            }
        }
        result = services.join_next() => {
            services.abort_all();
            while services.join_next().await.is_some() {}
            return match result {
                Some(Ok(name)) => Err(format!("critical Cost Engine workflow exited unexpectedly: {name}")),
                Some(Err(error)) => Err(format!("critical Cost Engine workflow panicked: {error}")),
                None => Err("Cost Engine started without critical workflows".to_string()),
            };
        }
    }

    let graceful = async {
        while let Some(result) = services.join_next().await {
            if let Err(error) = result {
                return Err(format!("Cost Engine workflow shutdown failed: {error}"));
            }
        }
        Ok(())
    };
    match tokio::time::timeout(std::time::Duration::from_secs(15), graceful).await {
        Ok(result) => result,
        Err(_) => {
            services.abort_all();
            Err("Cost Engine workflows exceeded the graceful shutdown deadline".to_string())
        }
    }
}
