// ======================================================================================================
// 📂 user/device.rs — Device presence worker & Active sessions query
// ======================================================================================================

use crate::infra::iam_proto::auth::BulkTouchDevicesRequest;
use crate::infra::redis::SessionManager;
use crate::infra::shared_redis::SharedRedisBus;
use crate::observability::logger::Logger;
use prost::Message;
use redis::streams::StreamReadReply;
use redis::AsyncCommands;
use std::sync::Arc;
use std::time::Duration;
use tokio::time;

#[allow(dead_code)]
pub mod device_proto {
    tonic::include_proto!("iam.rpc");
}

const HEARTBEAT_REDIS_KEY: &str = "iam:device_heartbeats";
const HEARTBEAT_TEMP_KEY: &str = "iam:device_heartbeats_temp";
const PRESENCE_CHANNEL: &str = "iam.device.bulk_touch_presence";
const FLUSH_INTERVAL_SECS: u64 = 30;
const EVICTION_OUTBOX_STREAM: &str = "iam:device:eviction-outbox";
const EVICTION_OUTBOX_GROUP: &str = "acr-device-eviction-relay-v1";
const EVICTION_OUTBOX_CONSUMER: &str = "acr-device-eviction-relay";

/// [COMMENT]: Background worker định kỳ gom heartbeat từ Auth Redis và publish bulk sang Shared Redis Controlplane
pub async fn start_presence_flush_worker(
    redis_client: Arc<redis::Client>,
    shared_redis: Arc<SharedRedisBus>,
) {
    tokio::spawn(async move {
        let mut interval = time::interval(Duration::from_secs(FLUSH_INTERVAL_SECS));
        interval.tick().await;

        loop {
            interval.tick().await;

            let mut conn = match redis_client.get_multiplexed_tokio_connection().await {
                Ok(c) => c,
                Err(e) => {
                    Logger::sys_error(
                        "device.presence",
                        "Failed to get Redis conn",
                        &e.to_string(),
                    );
                    continue;
                }
            };

            let renamed: Result<(), redis::RedisError> = redis::cmd("RENAME")
                .arg(HEARTBEAT_REDIS_KEY)
                .arg(HEARTBEAT_TEMP_KEY)
                .query_async(&mut conn)
                .await;

            if let Err(e) = renamed {
                if !e.to_string().contains("no such key") {
                    Logger::sys_error(
                        "device.presence",
                        "Failed to RENAME heartbeat key",
                        &e.to_string(),
                    );
                }
                continue;
            }

            let raw: Vec<(String, String)> = match conn.hgetall(HEARTBEAT_TEMP_KEY).await {
                Ok(v) => v,
                Err(e) => {
                    Logger::sys_error(
                        "device.presence",
                        "Failed to HGETALL temp key",
                        &e.to_string(),
                    );
                    continue;
                }
            };

            let _: Result<(), _> = redis::cmd("DEL")
                .arg(HEARTBEAT_TEMP_KEY)
                .query_async(&mut conn)
                .await;

            if raw.is_empty() {
                continue;
            }

            let updates: Vec<_> = raw
                .into_iter()
                .filter_map(|(device_id, value)| {
                    let parts: Vec<&str> = value.splitn(3, '|').collect();
                    let last_seen_at: i64 = parts.first().and_then(|s| s.parse().ok()).unwrap_or(0);
                    let last_seen_ip = parts.get(1).unwrap_or(&"").to_string();
                    let last_seen_user_agent = parts.get(2).unwrap_or(&"").to_string();
                    Some(
                        crate::infra::iam_proto::auth::bulk_touch_devices_request::DeviceUpdate {
                            device_id,
                            last_seen_at,
                            last_seen_ip,
                            last_seen_user_agent,
                        },
                    )
                })
                .collect();

            let update_count = updates.len();
            let req = BulkTouchDevicesRequest { updates };
            let mut buf = Vec::with_capacity(req.encoded_len());
            if let Err(e) = req.encode(&mut buf) {
                Logger::sys_error(
                    "device.presence",
                    "Failed to encode BulkTouchDevicesRequest",
                    &e.to_string(),
                );
                continue;
            }

            match shared_redis.publish_event(PRESENCE_CHANNEL, buf).await {
                Ok(subscribers) if subscribers > 0 => {
                    Logger::sys_info(
                        "device.presence",
                        &format!(
                            "Published {} device heartbeats to Shared Redis",
                            update_count
                        ),
                    );
                }
                Ok(_) => Logger::sys_warn(
                    "device.presence",
                    "No Controlplane replica subscribed to device presence",
                    "redis_pubsub_no_consumer",
                ),
                Err(e) => {
                    Logger::sys_error(
                        "device.presence",
                        "Failed to publish heartbeats to Shared Redis",
                        &e,
                    );
                }
            }
        }
    });
}

// [COMMENT]: Relay Auth Redis outbox sang Shared Redis Stream. Source XADD được commit
// atomically cùng session eviction; ACK chỉ sau khi target stream đã nhận payload.
pub async fn start_eviction_outbox_relay(
    auth_redis: Arc<redis::Client>,
    shared_redis: Arc<SharedRedisBus>,
) -> Result<(), String> {
    let mut connection = auth_redis
        .get_multiplexed_tokio_connection()
        .await
        .map_err(|error| format!("open Auth Redis eviction outbox: {error}"))?;
    let group_result: redis::RedisResult<()> = redis::cmd("XGROUP")
        .arg("CREATE")
        .arg(EVICTION_OUTBOX_STREAM)
        .arg(EVICTION_OUTBOX_GROUP)
        .arg("0")
        .arg("MKSTREAM")
        .query_async(&mut connection)
        .await;
    if let Err(error) = group_result {
        if !error.to_string().contains("BUSYGROUP") {
            return Err(format!("create Auth Redis eviction outbox group: {error}"));
        }
    }

    tokio::spawn(async move {
        loop {
            let mut connection = match auth_redis.get_multiplexed_tokio_connection().await {
                Ok(value) => value,
                Err(error) => {
                    Logger::sys_error(
                        "device.eviction_relay",
                        "Failed to connect Auth Redis outbox",
                        &error.to_string(),
                    );
                    time::sleep(Duration::from_millis(500)).await;
                    continue;
                }
            };

            loop {
                let pending: redis::RedisResult<StreamReadReply> = redis::cmd("XREADGROUP")
                    .arg("GROUP")
                    .arg(EVICTION_OUTBOX_GROUP)
                    .arg(EVICTION_OUTBOX_CONSUMER)
                    .arg("COUNT")
                    .arg(32)
                    .arg("STREAMS")
                    .arg(EVICTION_OUTBOX_STREAM)
                    .arg("0")
                    .query_async(&mut connection)
                    .await;
                let pending = match pending {
                    Ok(value) => value,
                    Err(error) => {
                        Logger::sys_error(
                            "device.eviction_relay",
                            "Failed to read pending Auth Redis outbox",
                            &error.to_string(),
                        );
                        break;
                    }
                };
                if !pending.keys.is_empty() {
                    relay_eviction_entries(&shared_redis, &mut connection, pending).await;
                    continue;
                }

                let fresh: redis::RedisResult<StreamReadReply> = redis::cmd("XREADGROUP")
                    .arg("GROUP")
                    .arg(EVICTION_OUTBOX_GROUP)
                    .arg(EVICTION_OUTBOX_CONSUMER)
                    .arg("COUNT")
                    .arg(32)
                    .arg("BLOCK")
                    .arg(5_000)
                    .arg("STREAMS")
                    .arg(EVICTION_OUTBOX_STREAM)
                    .arg(">")
                    .query_async(&mut connection)
                    .await;
                match fresh {
                    Ok(value) => {
                        relay_eviction_entries(&shared_redis, &mut connection, value).await
                    }
                    Err(error) if error.is_timeout() => {}
                    Err(error) => {
                        Logger::sys_error(
                            "device.eviction_relay",
                            "Failed to read new Auth Redis outbox",
                            &error.to_string(),
                        );
                        break;
                    }
                }
            }
        }
    });
    Ok(())
}

async fn relay_eviction_entries(
    shared_redis: &Arc<SharedRedisBus>,
    auth_connection: &mut redis::aio::MultiplexedConnection,
    reply: StreamReadReply,
) {
    for key in reply.keys {
        for entry in key.ids {
            let lock_key = format!("iam:device:dispatch:eviction-outbox:{}", entry.id);
            let acquired: bool = match redis::cmd("SET")
                .arg(&lock_key)
                .arg("1")
                .arg("NX")
                .arg("PX")
                .arg(10_000)
                .query_async(auth_connection)
                .await
            {
                Ok(value) => value,
                Err(_) => false,
            };
            if !acquired {
                time::sleep(Duration::from_millis(250)).await;
                continue;
            }

            let payload = match entry
                .map
                .get("payload")
                .and_then(|value| redis::from_redis_value::<Vec<u8>>(value).ok())
            {
                Some(value) => value,
                None => {
                    Logger::sys_error(
                        "device.eviction_relay",
                        "Dropping Auth Redis outbox entry without payload",
                        &entry.id,
                    );
                    acknowledge_eviction_outbox(auth_connection, &entry.id).await;
                    continue;
                }
            };

            match shared_redis
                .append_stream("iam:device:evicted-events", payload)
                .await
            {
                Ok(_) => acknowledge_eviction_outbox(auth_connection, &entry.id).await,
                Err(error) => Logger::sys_error(
                    "device.eviction_relay",
                    "Shared Redis append failed; Auth outbox remains pending",
                    &error,
                ),
            }
        }
    }
}

async fn acknowledge_eviction_outbox(
    connection: &mut redis::aio::MultiplexedConnection,
    entry_id: &str,
) {
    let _: redis::RedisResult<()> = redis::pipe()
        .atomic()
        .cmd("XACK")
        .arg(EVICTION_OUTBOX_STREAM)
        .arg(EVICTION_OUTBOX_GROUP)
        .arg(entry_id)
        .ignore()
        .cmd("XDEL")
        .arg(EVICTION_OUTBOX_STREAM)
        .arg(entry_id)
        .ignore()
        .query_async(connection)
        .await;
}

/// [COMMENT]: Lấy danh sách các thiết bị đang active của user từ Auth Redis cho internal transport.
pub async fn get_active_devices_bytes(
    session_mgr: &Arc<SessionManager>,
    payload: &[u8],
) -> Vec<u8> {
    use crate::infra::iam_proto::auth::{
        ActiveDeviceEntry, GetActiveDevicesRequest, GetActiveDevicesResponse,
    };

    let req = match GetActiveDevicesRequest::decode(payload) {
        Ok(r) => r,
        Err(e) => {
            Logger::sys_error(
                "device.active",
                "Failed to decode GetActiveDevicesRequest",
                &e.to_string(),
            );
            return vec![];
        }
    };

    let active_sessions = match session_mgr.get_active_sessions(&req.user_id).await {
        Ok(sessions) => sessions,
        Err(e) => {
            Logger::sys_error(
                "device.active",
                "Failed to get active sessions",
                &e.to_string(),
            );
            return vec![];
        }
    };

    let active_devices = active_sessions
        .into_iter()
        .map(|(client_device_id, last_seen_at)| ActiveDeviceEntry {
            client_device_id,
            last_seen_at,
        })
        .collect();

    let res = GetActiveDevicesResponse { active_devices };
    let mut reply_payload = Vec::new();
    if res.encode(&mut reply_payload).is_ok() {
        reply_payload
    } else {
        vec![]
    }
}
