// ======================================================================================================
// 📂 transport/pubsub.rs — NATS Event Router
// ======================================================================================================

use crate::billing::claims::TokenManager;
use crate::config::Config;
use crate::infra::nats::Nats;
use crate::infra::redis::SessionManager;
use crate::observability::logger::Logger;
use crate::sre::claims::SreTokenManager;
use futures_util::StreamExt;
use std::sync::Arc;

/// [COMMENT]: NatsEventRouter chịu trách nhiệm đăng ký tất cả các subscription NATS Core
pub struct NatsEventRouter {
    nats_client: async_nats::Client,
    nats: Arc<Nats>,
    session_mgr: Arc<SessionManager>,
    token_mgr: Arc<TokenManager>,
    sre_token_mgr: Arc<SreTokenManager>,
    config: Config,
}

impl NatsEventRouter {
    pub fn new(
        nats_client: async_nats::Client,
        nats: Arc<Nats>,
        session_mgr: Arc<SessionManager>,
        token_mgr: Arc<TokenManager>,
        sre_token_mgr: Arc<SreTokenManager>,
        config: Config,
    ) -> Self {
        Self {
            nats_client,
            nats,
            session_mgr,
            token_mgr,
            sre_token_mgr,
            config,
        }
    }

    pub async fn start(&self) {
        Logger::sys_info(
            "nats.router",
            "Initializing NATS Event Router subscriptions...",
        );

        // 1. Subscribe: iam.auth.verify_user_trinity
        let nc = self.nats_client.clone();
        let session_mgr = self.session_mgr.clone();
        let token_mgr = self.token_mgr.clone();
        let nats = self.nats.clone();
        let config = self.config.clone();
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
                let session_mgr_clone = session_mgr.clone();
                let token_mgr_clone = token_mgr.clone();
                let nats_clone = nats.clone();
                let config_clone = config.clone();
                tokio::spawn(async move {
                    use prost::Message;
                    let payload =
                        match crate::infra::nats::trinity::VerifyUserTrinityTokenRequest::decode(
                            msg.payload.as_ref(),
                        ) {
                            Ok(r) => r,
                            Err(e) => {
                                Logger::sys_error(
                                    "auth.nats",
                                    "Failed to decode VerifyUserTrinityTokenRequest",
                                    &e.to_string(),
                                );
                                return;
                            }
                        };

                    let mut valid = false;
                    let mut user_id = String::new();

                    let cookie_header = format!(
                        "access_token={}; access_key={}; access_secret={}",
                        payload.access_token, payload.access_key, payload.access_secret
                    );

                    let verify_res = crate::user::verify::verify_edge_session(
                        &session_mgr_clone,
                        &token_mgr_clone,
                        &nats_clone,
                        &config_clone,
                        &cookie_header,
                        &std::collections::HashMap::new(),
                        "POST",
                        "/api/v1/auth/verify",
                    )
                    .await;

                    if let Some(claims) = verify_res.claims {
                        valid = true;
                        user_id = claims.uid;
                    }

                    let res = crate::infra::nats::trinity::VerifyUserTrinityTokenResponse {
                        valid,
                        user_id,
                    };

                    let mut reply_payload = Vec::new();
                    if res.encode(&mut reply_payload).is_ok() {
                        if let Some(reply_subject) = msg.reply {
                            let _ = nc_clone.publish(reply_subject, reply_payload.into()).await;
                        }
                    }
                });
            }
        });

        // 2. Subscribe: iam.auth.verify_admin_trinity
        let nc = self.nats_client.clone();
        let session_mgr = self.session_mgr.clone();
        let token_mgr = self.sre_token_mgr.clone();
        let config = self.config.clone();
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
                let session_mgr_clone = session_mgr.clone();
                let token_mgr_clone = token_mgr.clone();
                let config_clone = config.clone();
                tokio::spawn(async move {
                    use prost::Message;
                    let payload =
                        match crate::infra::nats::trinity::VerifyAdminTrinityTokenRequest::decode(
                            msg.payload.as_ref(),
                        ) {
                            Ok(r) => r,
                            Err(e) => {
                                Logger::sys_error(
                                    "auth.nats",
                                    "Failed to decode VerifyAdminTrinityTokenRequest",
                                    &e.to_string(),
                                );
                                return;
                            }
                        };

                    let mut valid = false;
                    let mut admin_id = String::new();

                    let cookie_header = format!(
                        "access_token={}; access_key={}; access_secret={}",
                        payload.access_token, payload.access_key, payload.access_secret
                    );

                    let verify_res = crate::sre::verify::verify_sre_edge_session(
                        &session_mgr_clone,
                        &token_mgr_clone,
                        &config_clone,
                        &cookie_header,
                        &std::collections::HashMap::new(),
                        "POST",
                        "/admin/auth/verify",
                    )
                    .await;

                    if let Some(claims) = verify_res.claims {
                        valid = true;
                        admin_id = claims.sub;
                    }

                    let res = crate::infra::nats::trinity::VerifyAdminTrinityTokenResponse {
                        valid,
                        admin_id,
                    };

                    let mut reply_payload = Vec::new();
                    if res.encode(&mut reply_payload).is_ok() {
                        if let Some(reply_subject) = msg.reply {
                            let _ = nc_clone.publish(reply_subject, reply_payload.into()).await;
                        }
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
                    let reply_payload =
                        crate::user::device::get_active_devices_bytes(&mgr_clone, &msg.payload)
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
                    let reply_payload =
                        crate::user::revoke::revoke_sessions_bytes(&mgr_clone, &msg.payload).await;
                    if let Some(reply_subject) = msg.reply {
                        let _ = nc_clone.publish(reply_subject, reply_payload.into()).await;
                    }
                });
            }
        });

        // 5. Subscribe: hierarchy.zone.invalidated
        let nc = self.nats_client.clone();
        tokio::spawn(async move {
            let mut sub = match nc.subscribe("hierarchy.zone.invalidated".to_string()).await {
                Ok(s) => s,
                Err(e) => {
                    Logger::sys_error(
                        "nats.router",
                        "Failed to subscribe to hierarchy.zone.invalidated",
                        &e.to_string(),
                    );
                    return;
                }
            };

            while let Some(msg) = sub.next().await {
                tokio::spawn(async move {
                    use prost::Message;
                    if let Ok(event) =
                        crate::infra::zone::zone_proto::ZoneInvalidatedEvent::decode(msg.payload)
                    {
                        crate::infra::zone::invalidate_zone(&event).await;
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
