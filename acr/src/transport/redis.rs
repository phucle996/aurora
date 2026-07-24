use crate::config::Config;
use crate::infra::redis::SessionManager;
use crate::infra::shared_redis::SharedRedisBus;
use crate::observability::logger::Logger;
use crate::sre::claims::SreTokenManager;
use crate::token::TokenManager;
use futures_util::StreamExt;
use prost::Message;
use redis::streams::StreamReadReply;
use redis::AsyncCommands;
use std::sync::Arc;
use std::time::Duration;
use uuid::Uuid;

const ACTIVE_SESSIONS_CHANNEL: &str = "iam.device.get_active_sessions";
const ACTIVE_SESSIONS_REPLY_PREFIX: &str = "iam.device.get_active_sessions.reply.";
const ZONE_INVALIDATION_CHANNEL: &str = "hierarchy.zone.invalidated";
const VERIFY_USER_TRINITY_CHANNEL: &str = "iam.auth.verify_user_trinity";
const VERIFY_USER_TRINITY_REPLY_PREFIX: &str = "iam.auth.verify_user_trinity.reply.";
const VERIFY_ADMIN_TRINITY_CHANNEL: &str = "iam.auth.verify_admin_trinity";
const VERIFY_ADMIN_TRINITY_REPLY_PREFIX: &str = "iam.auth.verify_admin_trinity.reply.";
const REVOKE_STREAM: &str = "iam:device:revoke-requests";
const REVOKE_GROUP: &str = "acr-device-runtime-v1";
const REVOKE_CONSUMER: &str = "acr-device-runtime";

// [COMMENT]: SharedRedisRouter nhận toàn bộ Central-internal auth/device/topology traffic.
// Query realtime dùng PubSub; security command dùng Stream consumer group để không mất lệnh khi pod restart.
pub struct SharedRedisRouter {
    shared_redis: Arc<redis::Client>,
    shared_bus: Arc<SharedRedisBus>,
    session_mgr: Arc<SessionManager>,
    token_mgr: Arc<TokenManager>,
    sre_token_mgr: Arc<SreTokenManager>,
    config: Config,
}

impl SharedRedisRouter {
    pub async fn start(
        shared_redis: Arc<redis::Client>,
        shared_bus: Arc<SharedRedisBus>,
        session_mgr: Arc<SessionManager>,
        token_mgr: Arc<TokenManager>,
        sre_token_mgr: Arc<SreTokenManager>,
        config: Config,
    ) -> Result<Arc<Self>, String> {
        let connection = shared_redis
            .get_async_connection()
            .await
            .map_err(|error| format!("open device PubSub connection: {error}"))?;
        let mut subscriber = connection.into_pubsub();
        subscriber
            .subscribe(ACTIVE_SESSIONS_CHANNEL)
            .await
            .map_err(|error| format!("subscribe active-session channel: {error}"))?;
        subscriber
            .subscribe(ZONE_INVALIDATION_CHANNEL)
            .await
            .map_err(|error| format!("subscribe zone invalidation channel: {error}"))?;
        subscriber
            .subscribe(VERIFY_USER_TRINITY_CHANNEL)
            .await
            .map_err(|error| format!("subscribe user trinity channel: {error}"))?;
        subscriber
            .subscribe(VERIFY_ADMIN_TRINITY_CHANNEL)
            .await
            .map_err(|error| format!("subscribe admin trinity channel: {error}"))?;

        let mut stream_connection = shared_redis
            .get_multiplexed_tokio_connection()
            .await
            .map_err(|error| format!("open device Stream connection: {error}"))?;
        let create_result: redis::RedisResult<()> = redis::cmd("XGROUP")
            .arg("CREATE")
            .arg(REVOKE_STREAM)
            .arg(REVOKE_GROUP)
            .arg("0")
            .arg("MKSTREAM")
            .query_async(&mut stream_connection)
            .await;
        if let Err(error) = create_result {
            if !error.to_string().contains("BUSYGROUP") {
                return Err(format!("create device revoke consumer group: {error}"));
            }
        }

        let router = Arc::new(Self {
            shared_redis,
            shared_bus,
            session_mgr,
            token_mgr,
            sre_token_mgr,
            config,
        });
        router.start_pubsub_listener(subscriber);
        router.start_revoke_stream_worker();
        Ok(router)
    }

    fn start_pubsub_listener(&self, initial: redis::aio::PubSub) {
        let client = self.shared_redis.clone();
        let shared_bus = self.shared_bus.clone();
        let session_mgr = self.session_mgr.clone();
        let token_mgr = self.token_mgr.clone();
        let sre_token_mgr = self.sre_token_mgr.clone();
        let config = self.config.clone();
        tokio::spawn(async move {
            let mut active = Some(initial);
            loop {
                if let Some(mut subscriber) = active.take() {
                    let mut messages = subscriber.on_message();
                    while let Some(message) = messages.next().await {
                        if message.get_channel_name() == ZONE_INVALIDATION_CHANNEL {
                            if let Ok(event) =
                                crate::infra::zone::zone_proto::ZoneInvalidatedEvent::decode(
                                    message.get_payload_bytes(),
                                )
                            {
                                crate::infra::zone::invalidate_zone(&event).await;
                            }
                            continue;
                        }
                        let channel = message.get_channel_name().to_string();
                        let payload = message.get_payload_bytes().to_vec();
                        let client = client.clone();
                        let shared_bus = shared_bus.clone();
                        let session_mgr = session_mgr.clone();
                        let token_mgr = token_mgr.clone();
                        let sre_token_mgr = sre_token_mgr.clone();
                        let config = config.clone();
                        tokio::spawn(async move {
                            // [COMMENT]: Mọi ACR replica đều nhận PubSub; SETNX request_id
                            // chọn đúng một replica để đọc Auth Redis/Vault và trả response.
                            if payload.len() <= 16 {
                                return;
                            }
                            let request_id = match Uuid::from_slice(&payload[..16]) {
                                Ok(value) if value != Uuid::nil() => value,
                                _ => return,
                            };
                            let mut connection =
                                match client.get_multiplexed_tokio_connection().await {
                                    Ok(value) => value,
                                    Err(_) => return,
                                };
                            let lock_key = format!("iam:acr:dispatch:{channel}:{request_id}");
                            let acquired: bool = match redis::cmd("SET")
                                .arg(&lock_key)
                                .arg("1")
                                .arg("NX")
                                .arg("PX")
                                .arg(30_000)
                                .query_async(&mut connection)
                                .await
                            {
                                Ok(value) => value,
                                Err(_) => return,
                            };
                            if !acquired {
                                return;
                            }

                            let (reply_channel, response) = match channel.as_str() {
                                ACTIVE_SESSIONS_CHANNEL => (
                                    format!("{ACTIVE_SESSIONS_REPLY_PREFIX}{request_id}"),
                                    crate::user::device::get_active_devices_bytes(
                                        &session_mgr,
                                        &payload[16..],
                                    )
                                    .await,
                                ),
                                VERIFY_USER_TRINITY_CHANNEL => {
                                    let request = match crate::infra::iam_proto::trinity::VerifyUserTrinityTokenRequest::decode(&payload[16..]) {
                                        Ok(value) => value,
                                        Err(_) => return,
                                    };
                                    let cookie_header = format!(
                                        "access_token={}; access_key={}; access_secret={}",
                                        request.access_token,
                                        request.access_key,
                                        request.access_secret
                                    );
                                    let verification = crate::user::verify::verify_edge_session(
                                        &session_mgr,
                                        &token_mgr,
                                        client.as_ref(),
                                        &shared_bus,
                                        &config,
                                        &cookie_header,
                                        &std::collections::HashMap::new(),
                                        "POST",
                                        "/api/v1/auth/verify",
                                    )
                                    .await;
                                    let response = crate::infra::iam_proto::trinity::VerifyUserTrinityTokenResponse {
                                        valid: verification.claims.is_some(),
                                        user_id: verification.claims.map(|claims| claims.uid).unwrap_or_default(),
                                    };
                                    let mut wire = Vec::with_capacity(response.encoded_len());
                                    if response.encode(&mut wire).is_err() {
                                        return;
                                    }
                                    (
                                        format!("{VERIFY_USER_TRINITY_REPLY_PREFIX}{request_id}"),
                                        wire,
                                    )
                                }
                                VERIFY_ADMIN_TRINITY_CHANNEL => {
                                    let request = match crate::infra::iam_proto::trinity::VerifyAdminTrinityTokenRequest::decode(&payload[16..]) {
                                        Ok(value) => value,
                                        Err(_) => return,
                                    };
                                    let cookie_header = format!(
                                        "access_token={}; access_key={}; access_secret={}",
                                        request.access_token,
                                        request.access_key,
                                        request.access_secret
                                    );
                                    let verification = crate::sre::verify::verify_sre_edge_session(
                                        &session_mgr,
                                        &sre_token_mgr,
                                        &config,
                                        &cookie_header,
                                        &std::collections::HashMap::new(),
                                        "POST",
                                        "/admin/auth/verify",
                                    )
                                    .await;
                                    let response = crate::infra::iam_proto::trinity::VerifyAdminTrinityTokenResponse {
                                        valid: verification.claims.is_some(),
                                        admin_id: verification.claims.map(|claims| claims.sub).unwrap_or_default(),
                                    };
                                    let mut wire = Vec::with_capacity(response.encoded_len());
                                    if response.encode(&mut wire).is_err() {
                                        return;
                                    }
                                    (
                                        format!("{VERIFY_ADMIN_TRINITY_REPLY_PREFIX}{request_id}"),
                                        wire,
                                    )
                                }
                                _ => return,
                            };
                            let _: redis::RedisResult<i64> =
                                connection.publish(reply_channel, response).await;
                        });
                    }
                }

                Logger::sys_warn(
                    "shared_redis.device_router",
                    "Active-session PubSub disconnected; reconnecting",
                    "redis_pubsub_disconnected",
                );
                tokio::time::sleep(Duration::from_millis(500)).await;
                match client.get_async_connection().await {
                    Ok(connection) => {
                        let mut subscriber = connection.into_pubsub();
                        if subscriber.subscribe(ACTIVE_SESSIONS_CHANNEL).await.is_ok()
                            && subscriber
                                .subscribe(ZONE_INVALIDATION_CHANNEL)
                                .await
                                .is_ok()
                            && subscriber
                                .subscribe(VERIFY_USER_TRINITY_CHANNEL)
                                .await
                                .is_ok()
                            && subscriber
                                .subscribe(VERIFY_ADMIN_TRINITY_CHANNEL)
                                .await
                                .is_ok()
                        {
                            active = Some(subscriber);
                        }
                    }
                    Err(error) => Logger::sys_error(
                        "shared_redis.device_router",
                        "Failed to reconnect active-session PubSub",
                        &error.to_string(),
                    ),
                }
            }
        });
    }

    fn start_revoke_stream_worker(&self) {
        let client = self.shared_redis.clone();
        let session_mgr = self.session_mgr.clone();
        tokio::spawn(async move {
            loop {
                let mut connection = match client.get_multiplexed_tokio_connection().await {
                    Ok(value) => value,
                    Err(error) => {
                        Logger::sys_error(
                            "shared_redis.device_revoke",
                            "Failed to connect Shared Redis Stream",
                            &error.to_string(),
                        );
                        tokio::time::sleep(Duration::from_millis(500)).await;
                        continue;
                    }
                };

                loop {
                    // [COMMENT]: Tất cả replica dùng chung consumer identity; đọc ID=0 trước
                    // giúp replica sống tiếp quản pending entry của pod đã chết mà không cần XAUTOCLAIM.
                    let pending: redis::RedisResult<StreamReadReply> = redis::cmd("XREADGROUP")
                        .arg("GROUP")
                        .arg(REVOKE_GROUP)
                        .arg(REVOKE_CONSUMER)
                        .arg("COUNT")
                        .arg(32)
                        .arg("STREAMS")
                        .arg(REVOKE_STREAM)
                        .arg("0")
                        .query_async(&mut connection)
                        .await;
                    let pending = match pending {
                        Ok(reply) => reply,
                        Err(error) => {
                            Logger::sys_error(
                                "shared_redis.device_revoke",
                                "Failed to read pending revoke messages",
                                &error.to_string(),
                            );
                            break;
                        }
                    };
                    // [COMMENT]: Kiểm tra xem có entry pending thực sự nào trong key.ids không
                    // (tránh bug !pending.keys.is_empty() = true khi ids rỗng gây busy spin-loop).
                    let has_pending = pending.keys.iter().any(|key| !key.ids.is_empty());
                    if has_pending {
                        process_revoke_entries(&session_mgr, &mut connection, pending).await;
                        continue;
                    }

                    let fresh: redis::RedisResult<StreamReadReply> = redis::cmd("XREADGROUP")
                        .arg("GROUP")
                        .arg(REVOKE_GROUP)
                        .arg(REVOKE_CONSUMER)
                        .arg("COUNT")
                        .arg(32)
                        .arg("BLOCK")
                        .arg(5_000)
                        .arg("STREAMS")
                        .arg(REVOKE_STREAM)
                        .arg(">")
                        .query_async(&mut connection)
                        .await;
                    match fresh {
                        Ok(reply) => {
                            process_revoke_entries(&session_mgr, &mut connection, reply).await
                        }
                        Err(error) if error.is_timeout() => {}
                        Err(error) => {
                            Logger::sys_error(
                                "shared_redis.device_revoke",
                                "Failed to read new revoke messages",
                                &error.to_string(),
                            );
                            break;
                        }
                    }
                }
            }
        });
    }
}

async fn process_revoke_entries(
    session_mgr: &Arc<SessionManager>,
    connection: &mut redis::aio::MultiplexedConnection,
    reply: StreamReadReply,
) {
    for key in reply.keys {
        for entry in key.ids {
            // [COMMENT]: Shared consumer identity cho phép takeover pending entry nhưng có
            // thể làm nhiều replica nhìn cùng ID; short lock chặn duplicate Auth Redis work.
            let lock_key = format!("iam:device:dispatch:revoke-stream:{}", entry.id);
            let acquired: bool = match redis::cmd("SET")
                .arg(&lock_key)
                .arg("1")
                .arg("NX")
                .arg("PX")
                .arg(10_000)
                .query_async(connection)
                .await
            {
                Ok(value) => value,
                Err(_) => false,
            };
            if !acquired {
                tokio::time::sleep(Duration::from_millis(250)).await;
                continue;
            }

            let payload = match entry
                .map
                .get("payload")
                .and_then(|value| redis::from_redis_value::<Vec<u8>>(value).ok())
            {
                Some(value) => value,
                None => {
                    // [COMMENT]: Payload không đúng contract là poison message; ACK+DEL để
                    // không chặn toàn consumer, đồng thời log để producer được sửa.
                    Logger::sys_error(
                        "shared_redis.device_revoke",
                        "Dropping revoke message without binary payload",
                        &entry.id,
                    );
                    acknowledge_revoke(connection, &entry.id).await;
                    continue;
                }
            };

            let request =
                match crate::user::device::device_proto::RevokeUserSessionsByDevicesRequest::decode(
                    payload.as_slice(),
                ) {
                    Ok(value) => value,
                    Err(error) => {
                        Logger::sys_error(
                            "shared_redis.device_revoke",
                            "Dropping malformed revoke protobuf",
                            &error.to_string(),
                        );
                        acknowledge_revoke(connection, &entry.id).await;
                        continue;
                    }
                };

            match crate::user::revoke::revoke_sessions_by_devices(session_mgr, &request).await {
                Ok(_) => acknowledge_revoke(connection, &entry.id).await,
                Err(error) => {
                    // [COMMENT]: Không ACK khi Auth Redis lỗi; entry giữ pending và sẽ được
                    // replica ACR khác dùng cùng consumer identity retry ở vòng kế tiếp.
                    Logger::sys_error(
                        "shared_redis.device_revoke",
                        "Revoke execution failed; message remains pending",
                        &error,
                    );
                }
            }
        }
    }
}

async fn acknowledge_revoke(connection: &mut redis::aio::MultiplexedConnection, entry_id: &str) {
    let _: redis::RedisResult<()> = redis::pipe()
        .atomic()
        .cmd("XACK")
        .arg(REVOKE_STREAM)
        .arg(REVOKE_GROUP)
        .arg(entry_id)
        .ignore()
        .cmd("XDEL")
        .arg(REVOKE_STREAM)
        .arg(entry_id)
        .ignore()
        .query_async(connection)
        .await;
}
