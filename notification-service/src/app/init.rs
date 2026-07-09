use crate::config::Config;
use crate::handler::connect::AppState;
use crate::infra::centrifugo::CentrifugoClient;
use crate::infra::nats::NatsAuthClient;
use crate::infra::redis::RedisSubscriber;
use crate::observability::logger::Logger;
use std::sync::Arc;

// Khởi tạo toàn bộ tài nguyên kết nối hạ tầng và chạy ngầm các listener
pub async fn init_infrastructure(cfg: &Config) -> Arc<AppState> {
    // [ignoring loop detection]
    Logger::sys_info("infra.init", "Initializing infrastructure services...");

    // 1. Khởi tạo NATS client kết nối đến ACR service — xác thực Trinity Token qua NATS
    let auth_client = NatsAuthClient::new(
        cfg.nats_url.clone(),
        cfg.nats_ca_cert.clone(),
        cfg.nats_client_cert.clone(),
        cfg.nats_client_key.clone(),
    ).await;

    Logger::sys_info(
        "infra.nats",
        &format!(
            "ACR NATS auth client initialized → {}",
            cfg.nats_url
        ),
    );

    // 2. Khởi tạo Centrifugo HTTP client chuyên dụng phục vụ việc gửi thông báo real-time
    let centrifugo_client = CentrifugoClient::new(
        cfg.centrifugo_api_url.clone(),
        cfg.centrifugo_api_key.clone(),
    );

    // 3. Khởi tạo Redis Stream Subscriber và truyền centrifugo_client vào để xử lý tin nhắn
    let redis_sub = RedisSubscriber::new(&cfg.redis_url, centrifugo_client);

    // 4. Khởi chạy ngầm vòng lặp lắng nghe dòng sự kiện (Redis Stream) trong background thread
    tokio::spawn(async move {
        redis_sub.start_listening().await;
    });

    Arc::new(AppState { auth_client })
}
