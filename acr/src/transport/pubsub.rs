use crate::core::session::SessionManager;
use crate::observability::logger::Logger;
use crate::service::auth::AuthServiceImpl;
use futures_util::StreamExt;
use std::sync::Arc;

/// [COMMENT]: NatsEventRouter chịu trách nhiệm đăng ký tất cả các subscription NATS Core,
/// nhận dữ liệu nhị phân thô, phân luồng bằng tokio::spawn và chuyển giao cho Service tương ứng
/// xử lý trước khi xuất bản kết quả trả về.
pub struct NatsEventRouter {
    nats_client: async_nats::Client,
    session_mgr: Arc<SessionManager>,
    auth_svc: Arc<AuthServiceImpl>,
    zone_mgr: Arc<crate::core::zone::ZoneManager>,
}

impl NatsEventRouter {
    pub fn new(
        nats_client: async_nats::Client,
        session_mgr: Arc<SessionManager>,
        auth_svc: Arc<AuthServiceImpl>,
        zone_mgr: Arc<crate::core::zone::ZoneManager>,
    ) -> Self {
        Self {
            nats_client,
            session_mgr,
            auth_svc,
            zone_mgr,
        }
    }

    /// Khởi chạy toàn bộ các luồng lắng nghe NATS Core tập trung
    pub async fn start(&self) {
        Logger::sys_info(
            "nats.router",
            "Initializing NATS Event Router subscriptions...",
        );

        // 1. Subscribe: iam.auth.verify_user_trinity
        let nc = self.nats_client.clone();
        let svc = self.auth_svc.clone();
        tokio::spawn(async move {
            let mut sub = match nc
                .queue_subscribe(
                    "iam.auth.verify_user_trinity".to_string(),
                    "acr_auth_service".to_string(),
                )
                .await
            {
                Ok(s) => s,
                Err(e) => {
                    Logger::sys_error(
                        "nats.router",
                        "Failed to subscribe to iam.auth.verify_user_trinity",
                        &e.to_string(),
                    );
                    return;
                }
            };

            while let Some(msg) = sub.next().await {
                let nc_clone = nc.clone();
                let svc_clone = svc.clone();
                tokio::spawn(async move {
                    let reply_payload = svc_clone
                        .verify_user_trinity_token_bytes(&msg.payload)
                        .await;
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
            let mut sub = match nc
                .queue_subscribe(
                    "iam.auth.verify_admin_trinity".to_string(),
                    "acr_auth_service".to_string(),
                )
                .await
            {
                Ok(s) => s,
                Err(e) => {
                    Logger::sys_error(
                        "nats.router",
                        "Failed to subscribe to iam.auth.verify_admin_trinity",
                        &e.to_string(),
                    );
                    return;
                }
            };

            while let Some(msg) = sub.next().await {
                let nc_clone = nc.clone();
                let svc_clone = svc.clone();
                tokio::spawn(async move {
                    let reply_payload = svc_clone
                        .verify_admin_trinity_token_bytes(&msg.payload)
                        .await;
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
            let mut sub = match nc
                .queue_subscribe(
                    "iam.device.get_active_sessions".to_string(),
                    "acr_device_service".to_string(),
                )
                .await
            {
                Ok(s) => s,
                Err(e) => {
                    Logger::sys_error(
                        "nats.router",
                        "Failed to subscribe to iam.device.get_active_sessions",
                        &e.to_string(),
                    );
                    return;
                }
            };

            while let Some(msg) = sub.next().await {
                let nc_clone = nc.clone();
                let mgr_clone = mgr.clone();
                tokio::spawn(async move {
                    let reply_payload = crate::service::device::active::get_active_devices_bytes(
                        &mgr_clone,
                        &msg.payload,
                    )
                    .await;
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
            let mut sub = match nc
                .queue_subscribe(
                    "iam.device.revoke_sessions".to_string(),
                    "acr_device_service".to_string(),
                )
                .await
            {
                Ok(s) => s,
                Err(e) => {
                    Logger::sys_error(
                        "nats.router",
                        "Failed to subscribe to iam.device.revoke_sessions",
                        &e.to_string(),
                    );
                    return;
                }
            };

            while let Some(msg) = sub.next().await {
                let nc_clone = nc.clone();
                let mgr_clone = mgr.clone();
                tokio::spawn(async move {
                    let reply_payload = crate::service::device::revoke::revoke_sessions_bytes(
                        &mgr_clone,
                        &msg.payload,
                    )
                    .await;
                    if let Some(reply_subject) = msg.reply {
                        let _ = nc_clone.publish(reply_subject, reply_payload.into()).await;
                    }
                });
            }
        });

        // 5. Subscribe: core.zone.invalidated (Tất cả Edge Node phải nhận để update L1 Cache cục bộ)
        let nc = self.nats_client.clone();
        let zmgr = self.zone_mgr.clone();
        tokio::spawn(async move {
            let mut sub = match nc
                .subscribe("hierarchy.zone.invalidated".to_string())
                .await
            {
                Ok(s) => s,
                Err(e) => {
                    Logger::sys_error(
                        "nats.router",
                        "Failed to subscribe to core.zone.invalidated",
                        &e.to_string(),
                    );
                    return;
                }
            };

            while let Some(msg) = sub.next().await {
                let zmgr_clone = zmgr.clone();
                tokio::spawn(async move {
                    use prost::Message;
                    if let Ok(event) = crate::service::zone::client::zone_proto::ZoneInvalidatedEvent::decode(msg.payload) {
                        zmgr_clone.invalidate_zone(&event).await;
                    } else {
                        Logger::sys_error(
                            "nats.router",
                            "Failed to decode ZoneInvalidatedEvent from NATS",
                            "",
                        );
                    }
                });
            }
        });

        Logger::sys_info(
            "nats.router",
            "NATS Event Router subscriptions started successfully.",
        );
    }
}
