use std::time::Duration;
use clickhouse::Client as ClickhouseClient;
use tokio::signal;

// [COMMENT]: Khai báo các module của hệ thống
mod config;
mod infra;
mod service;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    println!("Bắt đầu khởi chạy Cost Manager Engine (Rust)...");

    // [COMMENT]: 1. Khởi tạo cấu hình ứng dụng
    let app_config = config::Config::from_env();

    // [COMMENT]: 2. Khởi tạo kết nối PostgreSQL (billing-psql) pool qua infra
    let pg_pool = infra::psql::init_pg_pool(&app_config).await?;
    println!("Kết nối thành công tới Postgres (billing-psql)!");

    // [COMMENT]: 3. Khởi tạo kết nối ClickHouse client hỗ trợ TLS/mTLS (HTTPS)
    let mut ch_http_builder = reqwest::Client::builder();

    // Nạp root cert cho ClickHouse
    if let Some(ref ca_path) = app_config.ch_ssl_root_cert {
        let ca_cert = std::fs::read(ca_path)?;
        let reqwest_cert = reqwest::Certificate::from_pem(&ca_cert)?;
        ch_http_builder = ch_http_builder.add_root_certificate(reqwest_cert);
    }
    // Nạp client credentials cho ClickHouse mTLS
    if let (Some(ref cert_path), Some(ref key_path)) = (&app_config.ch_ssl_client_cert, &app_config.ch_ssl_client_key) {
        let cert_pem = std::fs::read(cert_path)?;
        let key_pem = std::fs::read(key_path)?;
        let identity = reqwest::Identity::from_pem(&[cert_pem, key_pem].concat())?;
        ch_http_builder = ch_http_builder.identity(identity);
    }

    let ch_http_client = ch_http_builder.build()?;
    let ch_client = ClickhouseClient::with_http_client(ch_http_client)
        .with_url(&app_config.clickhouse_url)
        .with_database("storage");
    println!("Kết nối thành công tới ClickHouse!");

    // [COMMENT]: 4. Khởi tạo kết nối Redis multiplexed connection qua infra
    let redis_conn = infra::redis::init_redis_conn(&app_config).await?;
    println!("Kết nối thành công tới Redis!");

    // [COMMENT]: 5. Thiết lập watch channel cho Graceful Shutdown
    let (shutdown_tx, shutdown_rx) = tokio::sync::watch::channel(false);

    // [COMMENT]: 6. Khởi chạy các background services
    let services_config = app_config.clone();
    let services_pg_pool = pg_pool.clone();
    let services_ch_client = ch_client.clone();
    let services_redis_conn = redis_conn.clone();
    
    let services_handle = tokio::spawn(async move {
        service::register::run_services(
            services_config,
            services_pg_pool,
            services_ch_client,
            services_redis_conn,
            shutdown_rx,
        )
        .await;
    });

    // [COMMENT]: 7. Chờ tín hiệu kết thúc từ hệ điều hành (Ctrl+C hoặc SIGTERM)
    shutdown_signal().await;

    // [COMMENT]: 8. Gửi tín hiệu tắt hệ thống tới các services
    println!("Đang gửi tín hiệu dừng tới các background services...");
    let _ = shutdown_tx.send(true);

    // [COMMENT]: 9. Đợi các background tasks kết thúc (Graceful Shutdown)
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
