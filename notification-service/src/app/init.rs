use crate::config::Config;
use crate::handler::connect::AppState;
use crate::infra::centrifugo::CentrifugoClient;
use crate::infra::nats::NatsClient;
use crate::observability::logger::Logger;
use crate::listener::NatsListener;
use std::sync::Arc;

// Khởi tạo toàn bộ tài nguyên kết nối hạ tầng và chạy ngầm các listener
pub async fn init_infrastructure(cfg: &Config) -> Arc<AppState> {
    // [ignoring loop detection]
    Logger::sys_info("infra.init", "Initializing infrastructure services...");

    // 1. Khởi tạo NATS client kết nối đến NATS Core
    let nats_client = NatsClient::new(
        cfg.nats_url.clone(),
        cfg.nats_ca_cert.clone(),
        cfg.nats_client_cert.clone(),
        cfg.nats_client_key.clone(),
    ).await;

    Logger::sys_info(
        "infra.nats",
        &format!(
            "NATS client connection pool initialized → {}",
            cfg.nats_url
        ),
    );

    // 2. Khởi tạo Centrifugo HTTP client chuyên dụng phục vụ việc gửi thông báo real-time
    let centrifugo_client = CentrifugoClient::new(
        cfg.centrifugo_api_url.clone(),
        cfg.centrifugo_api_key.clone(),
    );

    // 3. Khởi tạo NATS Listener cho việc đồng bộ dung lượng và kết quả công việc
    let nats_listener = NatsListener::new(
        nats_client.clone(),
        centrifugo_client.clone(),
    );

    // 4. Khởi chạy ngầm vòng lặp lắng nghe NATS Core
    tokio::spawn(async move {
        nats_listener.start_listening().await;
    });

    Arc::new(AppState {
        nats_client,
        _centrifugo_client: centrifugo_client,
    })
}
