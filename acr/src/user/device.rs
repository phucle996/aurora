// ======================================================================================================
// 📂 user/device.rs — Device presence worker & Active sessions query
// ======================================================================================================

use crate::infra::nats::auth::BulkTouchDevicesRequest;
use crate::infra::redis::SessionManager;
use crate::observability::logger::Logger;
use prost::Message;
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
const NATS_SUBJECT: &str = "iam.device.bulk_touch_presence";
const FLUSH_INTERVAL_SECS: u64 = 30;

/// [COMMENT]: Background worker định kỳ gom heartbeat từ Redis và publish bulk sang NATS Controlplane
pub async fn start_presence_flush_worker(
    redis_client: Arc<redis::Client>,
    nats_client: async_nats::Client,
) {
    tokio::spawn(async move {
        let mut interval = time::interval(Duration::from_secs(FLUSH_INTERVAL_SECS));
        interval.tick().await;

        loop {
            interval.tick().await;

            let mut conn = match redis_client.get_multiplexed_tokio_connection().await {
                Ok(c) => c,
                Err(e) => {
                    Logger::sys_error("device.presence", "Failed to get Redis conn", &e.to_string());
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
                    Logger::sys_error("device.presence", "Failed to RENAME heartbeat key", &e.to_string());
                }
                continue;
            }

            let raw: Vec<(String, String)> = match conn.hgetall(HEARTBEAT_TEMP_KEY).await {
                Ok(v) => v,
                Err(e) => {
                    Logger::sys_error("device.presence", "Failed to HGETALL temp key", &e.to_string());
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
                    Some(crate::infra::nats::auth::bulk_touch_devices_request::DeviceUpdate {
                        device_id,
                        last_seen_at,
                        last_seen_ip,
                        last_seen_user_agent,
                    })
                })
                .collect();

            let update_count = updates.len();
            let req = BulkTouchDevicesRequest { updates };
            let mut buf = Vec::with_capacity(req.encoded_len());
            if let Err(e) = req.encode(&mut buf) {
                Logger::sys_error("device.presence", "Failed to encode BulkTouchDevicesRequest", &e.to_string());
                continue;
            }

            match nats_client.publish(NATS_SUBJECT.to_string(), buf.into()).await {
                Ok(_) => {
                    Logger::sys_info("device.presence", &format!("Published {} device heartbeats to NATS", update_count));
                }
                Err(e) => {
                    Logger::sys_error("device.presence", "Failed to publish heartbeats to NATS", &e.to_string());
                }
            }
        }
    });
}

/// [COMMENT]: Lấy danh sách các thiết bị đang active của user từ Redis L2 (cho gRPC / NATS)
pub async fn get_active_devices_bytes(session_mgr: &Arc<SessionManager>, payload: &[u8]) -> Vec<u8> {
    use crate::infra::nats::auth::{
        ActiveDeviceEntry, GetActiveDevicesRequest, GetActiveDevicesResponse,
    };

    let req = match GetActiveDevicesRequest::decode(payload) {
        Ok(r) => r,
        Err(e) => {
            Logger::sys_error("device.active", "Failed to decode GetActiveDevicesRequest", &e.to_string());
            return vec![];
        }
    };

    let active_sessions = match session_mgr.get_active_sessions(&req.user_id).await {
        Ok(sessions) => sessions,
        Err(e) => {
            Logger::sys_error("device.active", "Failed to get active sessions", &e.to_string());
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
