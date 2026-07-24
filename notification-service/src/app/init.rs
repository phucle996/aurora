use crate::config::Config;
use crate::handler::connect::AppState;
use crate::infra::centrifugo::CentrifugoClient;
use crate::infra::nats::NatsClient;
use crate::infra::redis::RedisSubscriber;
use crate::infra::shared_redis::SharedRedisRequestBus;
use crate::listener::NatsListener;
use crate::observability::logger::Logger;
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
    )
    .await;

    Logger::sys_info(
        "infra.nats",
        &format!("NATS client connection pool initialized → {}", cfg.nats_url),
    );
    let shared_redis = SharedRedisRequestBus::new(&cfg.shared_redis_url)
        .await
        .unwrap_or_else(|error| panic!("Failed to initialize Shared Redis auth bus: {error}"));

    // 2. Khởi tạo Centrifugo HTTP client chuyên dụng phục vụ việc gửi thông báo real-time
    let centrifugo_client = CentrifugoClient::new(
        cfg.centrifugo_api_url.clone(),
        cfg.centrifugo_api_key.clone(),
    );

    // NATS Core chỉ giữ soft-state/runtime từ Zone. Job completion nội vùng
    // Central được consume từ Shared L2 Redis Stream.
    let nats_listener = NatsListener::new(nats_client.clone(), centrifugo_client.clone());
    let redis_subscriber = RedisSubscriber::new(shared_redis.client(), centrifugo_client.clone());

    tokio::spawn(async move {
        nats_listener.start_listening().await;
    });
    tokio::spawn(async move {
        redis_subscriber.start_listening().await;
    });

    Arc::new(AppState {
        shared_redis,
        _centrifugo_client: centrifugo_client,
    })
}
