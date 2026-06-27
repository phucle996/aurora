use std::sync::Arc;
use crate::config::Config;
use crate::handler::connect::AppState;
use crate::infra::grpc::GrpcAuthClient;
use crate::infra::redis::RedisSubscriber;
use crate::infra::centrifugo::CentrifugoClient;
use crate::observability::logger::Logger;

// Khởi tạo toàn bộ tài nguyên kết nối hạ tầng và chạy ngầm các listener
pub async fn init_infrastructure(cfg: &Config) -> Arc<AppState> {
    // [ignoring loop detection]
    Logger::sys_info("infra.init", "Initializing infrastructure services...");

    // 1. Khởi tạo gRPC client kết nối đến ACR service (Rust) — xác thực Trinity Token
    let auth_client = GrpcAuthClient::new(
        cfg.acr_grpc_endpoint.clone(),
        cfg.acr_grpc_ca_cert.clone(),
        cfg.acr_grpc_client_cert.clone(),
        cfg.acr_grpc_client_key.clone(),
        cfg.acr_grpc_domain.clone(),
    );

    Logger::sys_info(
        "infra.grpc",
        &format!("ACR gRPC auth client initialized → {}", cfg.acr_grpc_endpoint),
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
