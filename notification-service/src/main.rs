// Khai báo các module cấu trúc của dịch vụ Notification Service
mod app;
mod config;
mod handler;
mod infra;
mod listener;
mod service;
mod observability;

use config::Config;
use observability::logger::Logger;
use observability::otel::OtelTracer;
use observability::metrics::MetricsManager;
use std::net::SocketAddr;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    // [COMMENT]: Thiết lập mặc định múi giờ TZ là Asia/Ho_Chi_Minh nếu chưa được định nghĩa
    if std::env::var("TZ").is_err() {
        std::env::set_var("TZ", "Asia/Ho_Chi_Minh");
    }

    // [ignoring loop detection]
    // Khởi tạo Logger đầu tiên để phục vụ ghi log hệ thống
    Logger::init();

    // Nạp cấu hình biến môi trường từ environment trước khi khởi tạo các dịch vụ khác
    let cfg = Config::from_env();
    Logger::sys_info(
        "system.config",
        &format!("Loaded configuration successfully: {:?}", cfg),
    );

    Logger::sys_info("system.startup", "Starting Notification Service (Rust)...");

    // Khởi tạo các thành phần thuộc hệ thống giám sát Observability đồng bộ với Dataplane sử dụng thông tin cấu hình
    OtelTracer::init(&cfg);
    MetricsManager::init();

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
