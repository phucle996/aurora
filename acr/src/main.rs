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
mod sre;
mod storage;
mod token;
mod transport;
mod user;

pub mod activity_proto {
    tonic::include_proto!("activity");
}

use crate::config::Config;
use crate::gateway::ext_authz::ExtAuthzService;
use crate::infra::redis::SessionManager;
use crate::observability::logger::Logger;
use crate::observability::otel::OtelTracer;
use crate::token::TokenManager;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let config = match Config::from_env() {
        Ok(c) => c,
        Err(e) => {
            eprintln!("Failed to load configuration: {}", e);
            std::process::exit(1);
        }
    };

    let vault_client = Arc::new(
        match crate::infra::vault::VaultClient::new(&config.vault).await {
            Ok(client) => client,
            Err(e) => {
                Logger::sys_error(
                    "main.vault",
                    "Failed to initialize Vault client",
                    &e.to_string(),
                );
                std::process::exit(1);
            }
        },
    );

    Logger::sys_info(
        "main.config",
        &format!(
            "Loaded config: grpc_port={}, redis_source=vault, shared_redis_source=vault, session_ttl={}s",
            config.grpc_port,
            config.session_ttl_secs
        ),
    );

    OtelTracer::init(&config);

    let redis_client = match crate::infra::redis::client_from_vault_with_mode(
        &vault_client,
        crate::infra::redis::AUTH_STATE_CONNECTION_PATH,
        config.auth_state_redis_mode,
    )
    .await
    {
        Ok(client) => client,
        Err(e) => {
            Logger::sys_error(
                "main.redis",
                "Failed to initialize Redis client",
                &e.to_string(),
            );
            std::process::exit(1);
        }
    };
    let shared_redis_client = match crate::infra::redis::client_from_vault_with_mode(
        &vault_client,
        crate::infra::redis::SHARED_L2_CONNECTION_PATH,
        config.shared_l2_redis_mode,
    )
    .await
    {
        Ok(client) => client,
        Err(e) => {
            Logger::sys_error(
                "main.shared_redis",
                "Failed to initialize Shared L2 Redis client",
                &e.to_string(),
            );
            std::process::exit(1);
        }
    };
    // [COMMENT]: Shared Redis bus phải subscribe reply pattern xong trước readiness;
    // nếu không login đầu tiên có thể publish trước khi ACR nhận được response.
    let shared_redis_bus =
        match crate::infra::shared_redis::SharedRedisBus::new(shared_redis_client.clone()).await {
            Ok(bus) => bus,
            Err(error) => {
                Logger::sys_error(
                    "main.shared_redis",
                    "Failed to initialize Shared Redis request bus",
                    &error,
                );
                std::process::exit(1);
            }
        };

    let session_mgr = Arc::new(SessionManager::new(redis_client.clone(), config.clone()));
    let token_mgr = Arc::new(TokenManager::new(vault_client.clone()));
    let oauth_service =
        match crate::user::oauth::OAuthProviderService::new(&config, vault_client.clone()).await {
            Ok(service) => Arc::new(service),
            Err(error) => {
                Logger::sys_error(
                    "main.oauth",
                    "Failed to initialize OAuth provider service",
                    &error,
                );
                std::process::exit(1);
            }
        };
    let sre_token_mgr = Arc::new(crate::sre::claims::SreTokenManager::new(
        vault_client.clone(),
        config.vault.admin_api_key_path.clone(),
    ));
    let ext_authz_service = ExtAuthzService::new(
        session_mgr.clone(),
        token_mgr.clone(),
        sre_token_mgr.clone(),
        config.clone(),
        shared_redis_client.clone(),
        shared_redis_bus.clone(),
        oauth_service,
    );

    // [COMMENT]: Mọi ACR↔Central request/event dùng Shared Redis; startup chỉ ready
    // sau khi auth/device/zone subscriptions và security stream group đã tồn tại.
    let _shared_redis_router = match crate::transport::redis::SharedRedisRouter::start(
        shared_redis_client.clone(),
        shared_redis_bus.clone(),
        session_mgr.clone(),
        token_mgr.clone(),
        sre_token_mgr.clone(),
        config.clone(),
    )
    .await
    {
        Ok(router) => router,
        Err(error) => {
            Logger::sys_error(
                "main.shared_redis",
                "Failed to initialize Shared Redis router",
                &error,
            );
            std::process::exit(1);
        }
    };

    crate::user::device::start_presence_flush_worker(
        redis_client.clone(),
        shared_redis_bus.clone(),
    )
    .await;
    if let Err(error) = crate::user::device::start_eviction_outbox_relay(
        redis_client.clone(),
        shared_redis_bus.clone(),
    )
    .await
    {
        Logger::sys_error(
            "main.shared_redis",
            "Failed to initialize durable device eviction relay",
            &error,
        );
        std::process::exit(1);
    }

    let addr: SocketAddr = format!("0.0.0.0:{}", config.grpc_port)
        .parse()
        .map_err(|e| format!("Invalid socket address: {}", e))?;

    Logger::sys_info(
        "main.server",
        &format!("Starting gRPC server on address: {}", addr),
    );

    Server::builder()
        .add_service(AuthorizationServer::new(ext_authz_service))
        .serve(addr)
        .await?;

    Ok(())
}
