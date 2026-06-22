use std::net::SocketAddr;
use std::sync::Arc;
use tonic::transport::Server;

// Import server type tự động sinh bởi Envoy gRPC
use envoy_types::pb::envoy::service::auth::v3::authorization_server::AuthorizationServer;

mod config;
mod error;
mod core;
mod authz;
mod service;
mod observability;
mod infra;

use crate::config::Config;
use crate::core::session::SessionManager;
use crate::core::token::TokenManager;
use crate::authz::evaluator::PolicyEvaluator;
use crate::service::ext_authz::ExtAuthzService;
use crate::service::session::SessionServiceImpl;
use crate::service::session::session_proto::session_service_server::SessionServiceServer;
use crate::observability::logger::Logger;
use crate::observability::otel::OtelTracer;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    // 1. Khởi tạo hệ thống Structured JSON Logger
    Logger::init();

    Logger::sys_info("main.bootstrap", "Bootstrapping Access Control Lifecycle (ACL) service...");

    // 2. Tải cấu hình từ Environment Variables
    let config = match Config::from_env() {
        Ok(cfg) => cfg,
        Err(e) => {
            Logger::sys_error("main.config", "Failed to load configuration", &e.to_string());
            std::process::exit(1);
        }
    };

    Logger::sys_info("main.config", &format!("Loaded config: grpc_port={}, redis={}, session_ttl={}s, grace={}s",
        config.grpc_port, config.redis_url, config.session_ttl_secs, config.grace_period_secs));

    // 3. Khởi tạo OpenTelemetry (Tracing + Metrics pipeline)
    OtelTracer::init(&config);

    // 4. Khởi tạo kết nối tới cụm Redis L2
    let redis_client = match redis::Client::open(config.redis_url.clone()) {
        Ok(client) => Arc::new(client),
        Err(e) => {
            Logger::sys_error("main.redis", "Failed to initialize Redis client", &e.to_string());
            std::process::exit(1);
        }
    };

    // 5.5. Khởi tạo Vault Client
    let vault_client = match crate::infra::vault::VaultClient::new(&config.vault).await {
        Ok(client) => Arc::new(client),
        Err(e) => {
            Logger::sys_error("main.vault", "Failed to initialize Vault client", &e.to_string());
            std::process::exit(1);
        }
    };

    // 6. Khởi tạo các Core Components và Service Layer
    let session_mgr = Arc::new(SessionManager::new(redis_client.clone(), config.clone()));
    let token_mgr = Arc::new(TokenManager::new(vault_client.clone()));
    let evaluator = Arc::new(PolicyEvaluator::new());


    let ext_authz_service = ExtAuthzService::new(
        session_mgr.clone(),
        token_mgr.clone(),
        evaluator.clone(),
        config.clone(),
    );

    let session_service = SessionServiceImpl::new(
        session_mgr.clone(),
        token_mgr.clone(),
        config.session_ttl_secs,
    );

    // 7. Cấu hình địa chỉ mạng và khởi chạy gRPC Server
    let addr: SocketAddr = format!("0.0.0.0:{}", config.grpc_port)
        .parse()
        .map_err(|e| format!("Invalid socket address: {}", e))?;

    Logger::sys_info("main.server", &format!("Starting ext_authz & session gRPC Server on: {}", addr));

    // 8. Dựng server kèm cơ chế Shutdown tín hiệu (Graceful Shutdown)
    Server::builder()
        .add_service(AuthorizationServer::new(ext_authz_service))
        .add_service(SessionServiceServer::new(session_service))
        .serve_with_shutdown(addr, async {
            // Lắng nghe tín hiệu ngắt (SIGINT/SIGTERM) để tắt server an toàn
            tokio::signal::ctrl_c()
                .await
                .expect("Failed to listen for ctrl_c signal");
            Logger::sys_info("main.shutdown", "Received shutdown signal. Cleaning up...");
        })
        .await?;


    // 9. Graceful Shutdown: Flush hết OTel data trước khi exit
    OtelTracer::stop();
    Logger::sys_info("main.shutdown", "ACL Service stopped gracefully.");

    Ok(())
}
