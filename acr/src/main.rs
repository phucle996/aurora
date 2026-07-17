use std::net::SocketAddr;
use std::sync::Arc;
use tonic::transport::Server;

// Import server type tự động sinh bởi Envoy gRPC
use envoy_types::pb::envoy::service::auth::v3::authorization_server::AuthorizationServer;
mod billing;
mod config;
mod error;
mod gateway;
mod infra;
mod observability;
pub mod pkg;
mod rpc;
mod sre;
mod transport;
mod user;

use crate::billing::claims::TokenManager;
use crate::config::Config;
use crate::gateway::ext_authz::ExtAuthzService;
use crate::infra::redis::SessionManager;
use crate::observability::logger::Logger;
use crate::observability::otel::OtelTracer;
use crate::rpc::session::DeviceRpcHandler;
use crate::user::device::device_proto::device_service_server::DeviceServiceServer;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let config = match Config::from_env() {
        Ok(c) => c,
        Err(e) => {
            eprintln!("Failed to load configuration: {}", e);
            std::process::exit(1);
        }
    };

    Logger::sys_info(
        "main.config",
        &format!(
            "Loaded config: grpc_port={}, redis={}, session_ttl={}s, grace=5s (hardcoded)",
            config.grpc_port, config.redis_url, config.session_ttl_secs
        ),
    );

    OtelTracer::init(&config);

    let redis_client = match redis::Client::open(config.redis_url.clone()) {
        Ok(client) => Arc::new(client),
        Err(e) => {
            Logger::sys_error(
                "main.redis",
                "Failed to initialize Redis client",
                &e.to_string(),
            );
            std::process::exit(1);
        }
    };

    let vault_client = match crate::infra::vault::VaultClient::new(&config.vault).await {
        Ok(client) => Arc::new(client),
        Err(e) => {
            Logger::sys_error(
                "main.vault",
                "Failed to initialize Vault client",
                &e.to_string(),
            );
            std::process::exit(1);
        }
    };

    let session_mgr = Arc::new(SessionManager::new(redis_client.clone(), config.clone()));
    let token_mgr = Arc::new(TokenManager::new(
        vault_client.clone(),
    ));
    let sre_token_mgr = Arc::new(crate::sre::claims::SreTokenManager::new(
        vault_client.clone(),
        config.vault.admin_api_key_path.clone(),
    ));
    let nats = Arc::new(
        crate::infra::nats::Nats::new(
            config.nats_url.clone(),
            config.nats_ca_cert.clone(),
            config.nats_client_cert.clone(),
            config.nats_client_key.clone(),
        )
        .await,
    );

    let ext_authz_service = ExtAuthzService::new(
        session_mgr.clone(),
        token_mgr.clone(),
        sre_token_mgr.clone(),
        config.clone(),
        nats.clone(),
    );

    let device_service = DeviceRpcHandler::new(session_mgr.clone());

    let nats_router = crate::transport::pubsub::NatsEventRouter::new(
        nats.client().clone(),
        nats.clone(),
        session_mgr.clone(),
        token_mgr.clone(),
        sre_token_mgr.clone(),
        config.clone(),
    );
    nats_router.start().await;

    crate::user::device::start_presence_flush_worker(redis_client.clone(), nats.client().clone())
        .await;

    let addr: SocketAddr = format!("0.0.0.0:{}", config.grpc_port)
        .parse()
        .map_err(|e| format!("Invalid socket address: {}", e))?;

    Logger::sys_info(
        "main.server",
        &format!("Starting gRPC server on address: {}", addr),
    );

    Server::builder()
        .add_service(AuthorizationServer::new(ext_authz_service))
        .add_service(DeviceServiceServer::new(device_service))
        .serve(addr)
        .await?;

    Ok(())
}
