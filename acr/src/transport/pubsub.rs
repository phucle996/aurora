use crate::core::session::SessionManager;
use crate::service::auth::AuthServiceImpl;
use crate::observability::logger::Logger;
use crate::infra::nats::trinity::{
    VerifyUserTrinityTokenRequest, VerifyUserTrinityTokenResponse,
    VerifyAdminTrinityTokenRequest, VerifyAdminTrinityTokenResponse,
};
use prost::Message;
use futures_util::StreamExt;
use std::sync::Arc;

/// [COMMENT]: NatsEventRouter chịu trách nhiệm đăng ký tất cả các subscription NATS Core,
/// nhận dữ liệu nhị phân thô, phân luồng bằng tokio::spawn và chuyển giao cho Service tương ứng
/// xử lý trước khi xuất bản kết quả trả về.
pub struct NatsEventRouter {
    nats_client: async_nats::Client,
    session_mgr: Arc<SessionManager>,
    auth_svc: Arc<AuthServiceImpl>,
}

impl NatsEventRouter {
    pub fn new(
        nats_client: async_nats::Client,
        session_mgr: Arc<SessionManager>,
        auth_svc: Arc<AuthServiceImpl>,
    ) -> Self {
        Self {
            nats_client,
            session_mgr,
            auth_svc,
        }
    }

    /// Khởi chạy toàn bộ các luồng lắng nghe NATS Core tập trung
    pub async fn start(&self) {
        Logger::sys_info("nats.router", "Initializing NATS Event Router subscriptions...");

        // 1. Subscribe: iam.auth.verify_user_trinity
        let nc = self.nats_client.clone();
        let svc = self.auth_svc.clone();
        tokio::spawn(async move {
            let mut sub = match nc.queue_subscribe(
                "iam.auth.verify_user_trinity".to_string(),
                "acr_auth_service".to_string(),
            ).await {
                Ok(s) => s,
                Err(e) => {
                    Logger::sys_error("nats.router", "Failed to subscribe to iam.auth.verify_user_trinity", &e.to_string());
                    return;
                }
            };

            while let Some(msg) = sub.next().await {
                let nc_clone = nc.clone();
                let svc_clone = svc.clone();
                tokio::spawn(async move {
                    let reply_payload = handle_verify_user_trinity(&svc_clone, &msg.payload).await;
                    if let Some(reply_subject) = msg.reply {
                        let _ = nc_clone.publish(reply_subject, reply_payload.into()).await;
                    }
                });
            }
        });

        // 2. Subscribe: iam.auth.verify_admin_trinity
        let nc = self.nats_client.clone();
        let svc = self.auth_svc.clone();
        tokio::spawn(async move {
            let mut sub = match nc.queue_subscribe(
                "iam.auth.verify_admin_trinity".to_string(),
                "acr_auth_service".to_string(),
            ).await {
                Ok(s) => s,
                Err(e) => {
                    Logger::sys_error("nats.router", "Failed to subscribe to iam.auth.verify_admin_trinity", &e.to_string());
                    return;
                }
            };

            while let Some(msg) = sub.next().await {
                let nc_clone = nc.clone();
                let svc_clone = svc.clone();
                tokio::spawn(async move {
                    let reply_payload = handle_verify_admin_trinity(&svc_clone, &msg.payload).await;
                    if let Some(reply_subject) = msg.reply {
                        let _ = nc_clone.publish(reply_subject, reply_payload.into()).await;
                    }
                });
            }
        });

        // 3. Subscribe: iam.device.get_active_sessions
        let nc = self.nats_client.clone();
        let mgr = self.session_mgr.clone();
        tokio::spawn(async move {
            let mut sub = match nc.queue_subscribe(
                "iam.device.get_active_sessions".to_string(),
                "acr_device_service".to_string(),
            ).await {
                Ok(s) => s,
                Err(e) => {
                    Logger::sys_error("nats.router", "Failed to subscribe to iam.device.get_active_sessions", &e.to_string());
                    return;
                }
            };

            while let Some(msg) = sub.next().await {
                let nc_clone = nc.clone();
                let mgr_clone = mgr.clone();
                tokio::spawn(async move {
                    let reply_payload = crate::service::device::active::get_active_devices_bytes(&mgr_clone, &msg.payload).await;
                    if let Some(reply_subject) = msg.reply {
                        let _ = nc_clone.publish(reply_subject, reply_payload.into()).await;
                    }
                });
            }
        });

        // 4. Subscribe: iam.device.revoke_sessions
        let nc = self.nats_client.clone();
        let mgr = self.session_mgr.clone();
        tokio::spawn(async move {
            let mut sub = match nc.queue_subscribe(
                "iam.device.revoke_sessions".to_string(),
                "acr_device_service".to_string(),
            ).await {
                Ok(s) => s,
                Err(e) => {
                    Logger::sys_error("nats.router", "Failed to subscribe to iam.device.revoke_sessions", &e.to_string());
                    return;
                }
            };

            while let Some(msg) = sub.next().await {
                let nc_clone = nc.clone();
                let mgr_clone = mgr.clone();
                tokio::spawn(async move {
                    let reply_payload = crate::service::device::revoke::revoke_user_sessions_by_devices_bytes(&mgr_clone, &msg.payload).await;
                    if let Some(reply_subject) = msg.reply {
                        let _ = nc_clone.publish(reply_subject, reply_payload.into()).await;
                    }
                });
            }
        });

        Logger::sys_info("nats.router", "NATS Event Router subscriptions started successfully.");
    }
}

/// Helper xử lý xác thực user và trả về byte payload
async fn handle_verify_user_trinity(auth_svc: &Arc<AuthServiceImpl>, payload: &[u8]) -> Vec<u8> {
    let req = match VerifyUserTrinityTokenRequest::decode(payload) {
        Ok(r) => r,
        Err(e) => {
            Logger::sys_error("nats.router", "Failed to decode VerifyUserTrinityTokenRequest", &e.to_string());
            return vec![];
        }
    };

    let res = match auth_svc.verify_user_trinity_token(req).await {
        Ok(r) => r,
        Err(_) => VerifyUserTrinityTokenResponse {
            valid: false,
            user_id: String::new(),
        },
    };

    let mut reply_payload = Vec::new();
    if res.encode(&mut reply_payload).is_ok() {
        reply_payload
    } else {
        vec![]
    }
}

/// Helper xử lý xác thực admin và trả về byte payload
async fn handle_verify_admin_trinity(auth_svc: &Arc<AuthServiceImpl>, payload: &[u8]) -> Vec<u8> {
    let req = match VerifyAdminTrinityTokenRequest::decode(payload) {
        Ok(r) => r,
        Err(e) => {
            Logger::sys_error("nats.router", "Failed to decode VerifyAdminTrinityTokenRequest", &e.to_string());
            return vec![];
        }
    };

    let res = match auth_svc.verify_admin_trinity_token(req).await {
        Ok(r) => r,
        Err(_) => VerifyAdminTrinityTokenResponse {
            valid: false,
            admin_id: String::new(),
        },
    };

    let mut reply_payload = Vec::new();
    if res.encode(&mut reply_payload).is_ok() {
        reply_payload
    } else {
        vec![]
    }
}
