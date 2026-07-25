// Khai báo các module cấu trúc của dịch vụ Notification Service
mod app;
mod config;
mod handler;
mod infra;
mod listener;
mod observability;
mod service;

use config::Config;
use observability::logger::Logger;
use observability::TelemetryRuntime;
use std::net::SocketAddr;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    // [COMMENT]: Thiết lập mặc định múi giờ TZ là Asia/Ho_Chi_Minh nếu chưa được định nghĩa
    if std::env::var("TZ").is_err() {
        std::env::set_var("TZ", "Asia/Ho_Chi_Minh");
    }

    // Nạp cấu hình biến môi trường từ environment trước khi khởi tạo các dịch vụ khác
    let cfg = Config::from_env();
    // Guard owns the bounded log writer and OTel providers. Keeping it in main
    // prevents early worker shutdown and guarantees final flush on SIGTERM.
    let _telemetry_guard = TelemetryRuntime::init(&cfg)?;
    Logger::sys_info(
        "system.config",
        "Configuration loaded with secrets redacted",
    );
    Logger::sys_info("system.startup", "Starting Notification Service");

    // Khởi tạo toàn bộ kết nối hạ tầng từ folder app/
    let app_state = app::init::init_infrastructure(&cfg).await;

    // Xây dựng router định tuyến HTTP Axum từ folder app/
    let app = app::router::build_router(app_state);

    let addr = SocketAddr::from(([0, 0, 0, 0], cfg.app_port));
    Logger::sys_info("system.web", &format!("Web server listening on {}", addr));

    let listener = tokio::net::TcpListener::bind(addr).await?;
    axum::serve(listener, app)
        .with_graceful_shutdown(shutdown_signal())
        .await?;

    Ok(())
}

async fn shutdown_signal() {
    let ctrl_c = async {
        if let Err(error) = tokio::signal::ctrl_c().await {
            Logger::sys_error(
                "system.shutdown_signal",
                "Failed to install Ctrl+C signal handler",
                &error.to_string(),
            );
        }
    };

    #[cfg(unix)]
    let terminate = async {
        match tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate()) {
            Ok(mut signal) => {
                signal.recv().await;
            }
            Err(error) => Logger::sys_error(
                "system.shutdown_signal",
                "Failed to install SIGTERM signal handler",
                &error.to_string(),
            ),
        }
    };

    #[cfg(not(unix))]
    let terminate = std::future::pending::<()>();

    tokio::select! {
        () = ctrl_c => {}
        () = terminate => {}
    }
    Logger::sys_info(
        "system.shutdown_signal",
        "Shutdown signal received; draining HTTP requests",
    );
}
