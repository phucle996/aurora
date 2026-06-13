// Khai báo các module cấu trúc của dịch vụ Notification Service
mod app;
mod config;
mod handler;
mod infra;
mod observability;

use config::Config;
use observability::logger::Logger;
use observability::otel::OtelTracer;
use observability::prometheus::PromRegistry;
use observability::resource::ResourceMonitor;
use std::net::SocketAddr;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    // [ignoring loop detection]
    // Khởi tạo các thành phần thuộc hệ thống giám sát Observability đồng bộ với Dataplane
    Logger::init();
    OtelTracer::init();
    PromRegistry::init();
    ResourceMonitor::start_monitor();

    Logger::sys_info("system.startup", "Starting Notification Service (Rust)...");

    // Nạp cấu hình biến môi trường từ environment
    let cfg = Config::from_env();
    Logger::sys_info(
        "system.config",
        &format!("Loaded configuration successfully: {:?}", cfg),
    );

    // Khởi tạo toàn bộ kết nối hạ tầng từ folder app/
    let app_state = app::init::init_infrastructure(&cfg).await;

    // Xây dựng router định tuyến HTTP Axum từ folder app/
    let app = app::router::build_router(app_state);

    let addr = SocketAddr::from(([0, 0, 0, 0], cfg.app_port));
    Logger::sys_info("system.web", &format!("Web server listening on {}", addr));

    let listener = tokio::net::TcpListener::bind(addr).await?;
    axum::serve(listener, app).await?;

    Ok(())
}
