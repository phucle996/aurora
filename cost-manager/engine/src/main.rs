use std::time::Duration;
use tokio::signal;

// [COMMENT]: Khai báo các module của hệ thống
mod config;
mod engine;
mod infra;
mod service;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    println!("Bắt đầu khởi chạy Cost Manager Engine (Rust)...");

    // [COMMENT]: 1. Khởi tạo cấu hình ứng dụng
    let mut app_config = config::Config::from_env()?;
    let vault = infra::vault::VaultClient::new(&app_config.vault).await?;
    infra::redis::resolve_from_vault(&vault, &mut app_config).await?;

    // [COMMENT]: 2. Khởi tạo kết nối PostgreSQL (billing-psql) pool qua infra
    let pg_pool = infra::psql::init_pg_pool(&vault, &app_config).await?;
    println!("Kết nối thành công tới Postgres (billing-psql)!");

    // [COMMENT]: Bootstrap pricing fail-closed trước khi bất kỳ billing worker nào được phép chạy.
    let pricing_runtime = engine::PricingRuntime::bootstrap(pg_pool.clone()).await?;
    println!("Đã bootstrap immutable Tier pricing catalog vào L1!");

    // [COMMENT]: 3. Khởi tạo kết nối Redis multiplexed connection qua infra
    let redis_conn = infra::redis::init_redis_conn(&vault, &app_config).await?;
    println!("Kết nối thành công tới Redis!");

    // [COMMENT]: 4. Thiết lập watch channel cho Graceful Shutdown
    let (shutdown_tx, shutdown_rx) = tokio::sync::watch::channel(false);

    // [COMMENT]: 5. Khởi chạy các background services
    let services_config = app_config.clone();
    let services_pg_pool = pg_pool.clone();
    let services_redis_conn = redis_conn.clone();
    let services_pricing_runtime = pricing_runtime.clone();

    let services_handle = tokio::spawn(async move {
        service::register::run_services(
            services_config,
            services_pg_pool,
            services_redis_conn,
            services_pricing_runtime,
            shutdown_rx,
        )
        .await;
    });

    // [COMMENT]: 6. Chờ tín hiệu kết thúc từ hệ điều hành (Ctrl+C hoặc SIGTERM)
    shutdown_signal().await;

    // [COMMENT]: 7. Gửi tín hiệu tắt hệ thống tới các services
    println!("Đang gửi tín hiệu dừng tới các background services...");
    let _ = shutdown_tx.send(true);

    // [COMMENT]: 8. Đợi các background tasks kết thúc (Graceful Shutdown)
    println!("Đợi các dịch vụ hoàn thành nốt công việc...");
    tokio::select! {
        _ = services_handle => {
            println!("Các background services đã tắt an toàn.");
        }
        _ = tokio::time::sleep(Duration::from_secs(15)) => {
            eprintln!("Cảnh báo: Hết thời gian chờ (15s), một số service chưa tắt hẳn. Tiến hành ép buộc dừng.");
        }
    }

    println!("Cost Manager Engine đã dừng hoàn toàn.");
    Ok(())
}

/// [COMMENT]: Hàm lắng nghe tín hiệu Ctrl+C (SIGINT) hoặc SIGTERM từ hệ điều hành để kích hoạt Shutdown
async fn shutdown_signal() {
    let ctrl_c = async {
        signal::ctrl_c()
            .await
            .expect("Không thể đăng ký bộ xử lý tín hiệu Ctrl+C");
    };

    #[cfg(unix)]
    let terminate = async {
        signal::unix::signal(signal::unix::SignalKind::terminate())
            .expect("Không thể đăng ký bộ xử lý tín hiệu SIGTERM")
            .recv()
            .await;
    };

    #[cfg(not(unix))]
    let terminate = std::future::pending::<()>();

    tokio::select! {
        _ = ctrl_c => {
            println!("Nhận được tín hiệu dừng Ctrl+C (SIGINT)...");
        }
        _ = terminate => {
            println!("Nhận được tín hiệu dừng SIGTERM...");
        }
    }
}
