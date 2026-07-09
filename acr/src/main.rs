use std::net::SocketAddr;
use std::sync::Arc;
use tonic::transport::Server;

// Import server type tự động sinh bởi Envoy gRPC
use envoy_types::pb::envoy::service::auth::v3::authorization_server::AuthorizationServer;

mod authz;
mod config;
mod core;
mod error;
mod infra;
mod observability;
pub mod pkg;
mod rpc;
mod service;

use crate::authz::evaluator::PolicyEvaluator;
use crate::config::Config;
use crate::core::session::SessionManager;
use crate::core::token::TokenManager;
use crate::observability::logger::Logger;
use crate::observability::otel::OtelTracer;
use crate::rpc::session::DeviceRpcHandler;
use crate::service::auth::auth_proto::auth_service_server::AuthServiceServer;
use crate::service::auth::AuthServiceImpl;
use crate::service::ext_authz::ExtAuthzService;
// [COMMENT]: Import DeviceServiceServer (đổi tên từ SessionServiceServer sau khi bỏ ReleaseTrinitySession RPC)
use crate::service::session::release_session::device_proto::device_service_server::DeviceServiceServer;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    // 1. Khởi tạo hệ thống Structured JSON Logger
    Logger::init();

    Logger::sys_info(
        "main.bootstrap",
        "Bootstrapping Access Context Resolution (ACR) service...",
    );

    // 2. Tải cấu hình từ Environment Variables
    let config = match Config::from_env() {
        Ok(cfg) => cfg,
        Err(e) => {
            Logger::sys_error(
                "main.config",
                "Failed to load configuration",
                &e.to_string(),
            );
            std::process::exit(1);
        }
    };

    // [COMMENT]: Log thông tin cấu hình, thời gian Grace Period đã được hardcode về 5s
    Logger::sys_info(
        "main.config",
        &format!(
            "Loaded config: grpc_port={}, redis={}, session_ttl={}s, grace=5s (hardcoded)",
            config.grpc_port, config.redis_url, config.session_ttl_secs
        ),
    );

    // 3. Khởi tạo OpenTelemetry (Tracing + Metrics pipeline)
    OtelTracer::init(&config);

    // 4. Khởi tạo kết nối tới cụm Redis L2
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

    // 5.5. Khởi tạo Vault Client
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

    // 6. Khởi tạo các Core Components và Service Layer
    let session_mgr = Arc::new(SessionManager::new(redis_client.clone(), config.clone()));
    let token_mgr = Arc::new(TokenManager::new(
        vault_client.clone(),
        config.vault.admin_api_key_path.clone(),
    ));
    let evaluator = Arc::new(PolicyEvaluator::new());

    // [COMMENT]: Khởi tạo NATS client để kết nối đến Control Plane qua NATS Core
    let nats = Arc::new(crate::infra::nats::Nats::new(config.nats_url.clone()).await);

    // [COMMENT]: Khởi tạo ZoneManager - L1 in-process + Redis L2 shared + gRPC fallback
    // Sau khi 1 node gọi gRPC và ghi Redis L2, các node khác tự đọc L2 mà không cần gRPC lại
    let zone_mgr = Arc::new(crate::core::zone::ZoneManager::new(
        nats.clone(),
        redis_client.clone(),
    ));

    let ext_authz_service = ExtAuthzService::new(
        session_mgr.clone(),
        token_mgr.clone(),
        evaluator.clone(),
        config.clone(),
        nats.clone(),
        zone_mgr.clone(),
    );

    // [COMMENT]: Khởi tạo DeviceRpcHandler – chỉ xử lý RevokeUserSessionsByDevices
    let device_service = DeviceRpcHandler::new(session_mgr.clone());

    let auth_service = AuthServiceImpl::new(
        session_mgr.clone(),
        token_mgr.clone(),
        redis_client.clone(),
        nats.clone(),
    );

    // [COMMENT]: Khởi chạy NATS subscriber để lắng nghe các yêu cầu xác thực Trinity Token từ Centrifugo/Notification Service
    let nats_client_clone = nats.client().clone();
    let auth_svc_ptr = Arc::new(auth_service.clone());

    let auth_svc_ptr1 = auth_svc_ptr.clone();
    let nats_client_clone1 = nats_client_clone.clone();
    tokio::spawn(async move {
        let mut subscriber = match nats_client_clone1
            .queue_subscribe(
                "iam.auth.verify_user_trinity".to_string(),
                "acr_auth_service".to_string(),
            )
            .await
        {
            Ok(sub) => sub,
            Err(e) => {
                Logger::sys_error(
                    "nats.subscriber",
                    "Failed to subscribe to iam.auth.verify_user_trinity",
                    &e.to_string(),
                );
                return;
            }
        };

        Logger::sys_info(
            "nats.subscriber",
            "Successfully subscribed to iam.auth.verify_user_trinity",
        );

        use futures_util::StreamExt;
        use prost::Message;
        while let Some(msg) = subscriber.next().await {
            let auth_svc = auth_svc_ptr1.clone();
            let nats_client = nats_client_clone1.clone();
            tokio::spawn(async move {
                let req = match crate::infra::nats::trinity::VerifyUserTrinityTokenRequest::decode(
                    msg.payload.as_ref(),
                ) {
                    Ok(r) => r,
                    Err(e) => {
                        Logger::sys_error(
                            "nats.subscriber",
                            "Failed to decode VerifyUserTrinityTokenRequest",
                            &e.to_string(),
                        );
                        return;
                    }
                };

                let res = match auth_svc.verify_user_trinity_token(req).await {
                    Ok(r) => r,
                    Err(_) => crate::infra::nats::trinity::VerifyUserTrinityTokenResponse {
                        valid: false,
                        role_id: String::new(),
                        user_id: String::new(),
                        zone_id: String::new(),
                    },
                };

                let mut reply_payload = Vec::new();
                if let Ok(_) = res.encode(&mut reply_payload) {
                    if let Some(reply_subject) = msg.reply {
                        let _ = nats_client
                            .publish(reply_subject, reply_payload.into())
                            .await;
                    }
                }
            });
        }
    });

    let auth_svc_ptr2 = auth_svc_ptr.clone();
    let nats_client_clone2 = nats_client_clone.clone();
    tokio::spawn(async move {
        let mut subscriber = match nats_client_clone2
            .queue_subscribe(
                "iam.auth.verify_admin_trinity".to_string(),
                "acr_auth_service".to_string(),
            )
            .await
        {
            Ok(sub) => sub,
            Err(e) => {
                Logger::sys_error(
                    "nats.subscriber",
                    "Failed to subscribe to iam.auth.verify_admin_trinity",
                    &e.to_string(),
                );
                return;
            }
        };

        Logger::sys_info(
            "nats.subscriber",
            "Successfully subscribed to iam.auth.verify_admin_trinity",
        );

        use futures_util::StreamExt;
        use prost::Message;
        while let Some(msg) = subscriber.next().await {
            let auth_svc = auth_svc_ptr2.clone();
            let nats_client = nats_client_clone2.clone();
            tokio::spawn(async move {
                let req = match crate::infra::nats::trinity::VerifyAdminTrinityTokenRequest::decode(
                    msg.payload.as_ref(),
                ) {
                    Ok(r) => r,
                    Err(e) => {
                        Logger::sys_error(
                            "nats.subscriber",
                            "Failed to decode VerifyAdminTrinityTokenRequest",
                            &e.to_string(),
                        );
                        return;
                    }
                };

                let res = match auth_svc.verify_admin_trinity_token(req).await {
                    Ok(r) => r,
                    Err(_) => crate::infra::nats::trinity::VerifyAdminTrinityTokenResponse {
                        valid: false,
                        role_id: String::new(),
                        user_id: String::new(),
                    },
                };

                let mut reply_payload = Vec::new();
                if let Ok(_) = res.encode(&mut reply_payload) {
                    if let Some(reply_subject) = msg.reply {
                        let _ = nats_client
                            .publish(reply_subject, reply_payload.into())
                            .await;
                    }
                }
            });
        }
    });

    // 7. Cấu hình địa chỉ mạng và khởi chạy gRPC Server
    let addr: SocketAddr = format!("0.0.0.0:{}", config.grpc_port)
        .parse()
        .map_err(|e| format!("Invalid socket address: {}", e))?;

    Logger::sys_info(
        "main.server",
        &format!(
            "Starting ext_authz, session & auth gRPC Server on: {}",
            addr
        ),
    );

    // 8. Dựng server kèm cơ chế Shutdown tín hiệu (Graceful Shutdown)
    Server::builder()
        .add_service(AuthorizationServer::new(ext_authz_service))
        .add_service(DeviceServiceServer::new(device_service))
        .add_service(AuthServiceServer::new(auth_service))
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
