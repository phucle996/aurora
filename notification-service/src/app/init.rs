use crate::config::Config;
use crate::handler::connect::AppState;
use crate::infra::centrifugo::CentrifugoClient;
use crate::infra::redis::RedisSubscriber;
use crate::infra::shared_redis::SharedRedisRequestBus;
use crate::listener::RealtimeListener;
use crate::observability::logger::Logger;
use std::sync::Arc;

// Khởi tạo toàn bộ tài nguyên kết nối hạ tầng và chạy ngầm các listener
pub async fn init_infrastructure(cfg: &Config) -> Arc<AppState> {
    // [ignoring loop detection]
    Logger::sys_info("infra.init", "Initializing infrastructure services...");

    let shared_redis = SharedRedisRequestBus::new(&cfg.shared_redis_url)
        .await
        .unwrap_or_else(|error| panic!("Failed to initialize Shared Redis auth bus: {error}"));

    // 2. Khởi tạo Centrifugo HTTP client chuyên dụng phục vụ việc gửi thông báo real-time
    let centrifugo_client = CentrifugoClient::new(
        cfg.centrifugo_api_url.clone(),
        cfg.centrifugo_api_key.clone(),
    );

    // JO terminates the cross-Zone NATS Core hop. Central realtime fan-out and
    // durable job completion use Shared Redis Pub/Sub and Stream respectively.
    let realtime_listener = RealtimeListener::new(shared_redis.client(), centrifugo_client.clone());
    let redis_subscriber = RedisSubscriber::new(shared_redis.client(), centrifugo_client.clone());

    tokio::spawn(async move {
        realtime_listener.start_listening().await;
    });
    tokio::spawn(async move {
        redis_subscriber.start_listening().await;
    });

    Arc::new(AppState {
        shared_redis,
        _centrifugo_client: centrifugo_client,
    })
}
