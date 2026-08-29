mod app;
mod config;
mod infra;
mod middleware;
mod observability;
mod repo;
mod service;
mod transport;

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

    let cfg = Config::from_env()?;
    let vault = infra::vault::VaultClient::new(&cfg.vault).await?;
    // Guard owns the bounded log writer and OTel providers. Keeping it in main
    // prevents early worker shutdown and guarantees final flush on SIGTERM.
    let _telemetry_guard = TelemetryRuntime::init(&cfg)?;
    Logger::sys_info(
        "system.config",
        "Configuration loaded with secrets redacted",
    );
    Logger::sys_info("system.startup", "Starting Notification Service");

    let runtime = app::bootstrap::Runtime::build(&cfg, &vault).await?;
    let app = app::router::build_router(runtime.state());

    let addr = SocketAddr::from(([0, 0, 0, 0], cfg.app_port));
    Logger::sys_info("system.web", &format!("Web server listening on {}", addr));

    let listener = match tokio::net::TcpListener::bind(addr).await {
        Ok(listener) => listener,
        Err(error) => {
            runtime.shutdown().await;
            return Err(error.into());
        }
    };
    let serve_result = axum::serve(listener, app)
        .with_graceful_shutdown(shutdown_signal())
        .await;
    runtime.shutdown().await;
    serve_result?;

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
