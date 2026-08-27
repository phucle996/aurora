use std::path::PathBuf;
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
    println!("Đã bootstrap immutable PAYG pricing schedule catalog vào L1!");

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

    let mut services_handle = tokio::spawn(async move {
        service::register::run_services(
            services_config,
            services_pg_pool,
            services_redis_conn,
            services_pricing_runtime,
            shutdown_rx,
        )
        .await
    });

    let ready_file = std::env::var("AURORA_ENGINE_READY_FILE")
        .ok()
        .map(|value| value.trim().to_owned())
        .filter(|value| !value.is_empty())
        .map(PathBuf::from);
    if let Some(path) = ready_file.as_ref() {
        let temporary = path.with_extension(format!("ready-{}", std::process::id()));
        std::fs::write(&temporary, b"ready\n")?;
        std::fs::rename(&temporary, path)?;
    }

    // The embedded API treats an unexpected exit as a pod-level failure. The
    // Engine must therefore also exit if any critical billing workflow stops;
    // keeping an idle parent alive would make readiness lie about charging.
    let service_failure = tokio::select! {
        _ = shutdown_signal() => None,
        result = &mut services_handle => {
            Some(match result {
                Ok(Ok(())) => "critical Cost Engine workflows exited unexpectedly".to_string(),
                Ok(Err(error)) => error,
                Err(error) => format!("Cost Engine workflow supervisor panicked: {error}"),
            })
        }
    };

    // [COMMENT]: 7. Gửi tín hiệu tắt hệ thống tới các services
    println!("Đang gửi tín hiệu dừng tới các background services...");
    let _ = shutdown_tx.send(true);

    // [COMMENT]: 8. Đợi các background tasks kết thúc (Graceful Shutdown)
    println!("Đợi các dịch vụ hoàn thành nốt công việc...");
    if service_failure.is_none() {
        tokio::select! {
            _ = &mut services_handle => {
                println!("Các background services đã tắt an toàn.");
            }
            _ = tokio::time::sleep(Duration::from_secs(20)) => {
                services_handle.abort();
                eprintln!("Cảnh báo: Hết thời gian chờ, Cost Engine supervisor bị hủy.");
            }
        }
    }

    if let Some(path) = ready_file.as_ref() {
        let _ = std::fs::remove_file(path);
    }
    if let Some(error) = service_failure {
        return Err(error.into());
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
