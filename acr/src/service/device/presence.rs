// ======================================================================================================
// 📂 MODULE: acr/src/service/device/presence.rs
//            Background worker định kỳ gom heartbeat từ Redis và gửi bulk sang Control Plane qua NATS
// ======================================================================================================

use crate::infra::nats::auth::BulkTouchDevicesRequest;
use crate::observability::logger::Logger;
use prost::Message;
use redis::AsyncCommands;
use std::sync::Arc;
use std::time::Duration;
use tokio::time;

// [COMMENT]: Hằng số cấu hình
const HEARTBEAT_REDIS_KEY: &str = "iam:device_heartbeats";
const HEARTBEAT_TEMP_KEY: &str = "iam:device_heartbeats_temp";
const NATS_SUBJECT: &str = "iam.device.bulk_touch_presence";
const FLUSH_INTERVAL_SECS: u64 = 30;

/// [COMMENT]: Khởi chạy background worker định kỳ gom heartbeat từ Redis,
/// encode Protobuf và publish qua NATS đến Control Plane.
/// Worker chạy bất đồng bộ hoàn toàn, không chặn luồng chính.
pub async fn start_presence_flush_worker(
    redis_client: Arc<redis::Client>,
    nats_client: async_nats::Client,
) {
    tokio::spawn(async move {
        let mut interval = time::interval(Duration::from_secs(FLUSH_INTERVAL_SECS));
        // [COMMENT]: Bỏ qua tick đầu tiên để tránh flush ngay khi khởi động
        interval.tick().await;

        loop {
            interval.tick().await;

            // [COMMENT]: Lấy kết nối async từ Redis pool
            let mut conn = match redis_client.get_multiplexed_tokio_connection().await {
                Ok(c) => c,
                Err(e) => {
                    Logger::sys_error(
                        "presence.worker",
                        "Failed to get Redis connection for heartbeat flush",
                        &e.to_string(),
                    );
                    continue;
                }
            };

            // [COMMENT]: Atomic swap: đổi tên key heartbeats -> heartbeats_temp để không bị race với
            // các request đang ghi đồng thời. Nếu key chính chưa tồn tại thì RENAME sẽ lỗi, ta skip.
            let renamed: Result<(), redis::RedisError> = redis::cmd("RENAME")
                .arg(HEARTBEAT_REDIS_KEY)
                .arg(HEARTBEAT_TEMP_KEY)
                .query_async(&mut conn)
                .await;

            if let Err(e) = renamed {
                // [COMMENT]: RENAME lỗi thường là do key không tồn tại (chưa có heartbeat nào) — đây là trường hợp bình thường.
                // Redis Rust client format thực tế: "ResponseError: no such key" (không có prefix ERR).
                if !e.to_string().contains("no such key") {
                    Logger::sys_error(
                        "presence.worker",
                        "Failed to RENAME heartbeat key",
                        &e.to_string(),
                    );
                }
                continue;
            }

            // [COMMENT]: Lấy toàn bộ cặp field-value từ hash tạm
            let raw: Vec<(String, String)> = match conn.hgetall(HEARTBEAT_TEMP_KEY).await {
                Ok(v) => v,
                Err(e) => {
                    Logger::sys_error(
                        "presence.worker",
                        "Failed to HGETALL heartbeat temp key",
                        &e.to_string(),
                    );
                    continue;
                }
            };

            // [COMMENT]: Xoá key tạm bất kể kết quả xử lý để tránh memory leak
            let _: Result<(), _> = redis::cmd("DEL")
                .arg(HEARTBEAT_TEMP_KEY)
                .query_async(&mut conn)
                .await;

            if raw.is_empty() {
                continue;
            }

            // [COMMENT]: Parse từng cặp "device_id" -> "timestamp|ip|user_agent"
            let updates: Vec<_> = raw
                .into_iter()
                .filter_map(|(device_id, value)| {
                    let parts: Vec<&str> = value.splitn(3, '|').collect();
                    let last_seen_at: i64 = parts.first().and_then(|s| s.parse().ok()).unwrap_or(0);
                    let last_seen_ip = parts.get(1).unwrap_or(&"").to_string();
                    let last_seen_user_agent = parts.get(2).unwrap_or(&"").to_string();
                    Some(
                        crate::infra::nats::auth::bulk_touch_devices_request::DeviceUpdate {
                            device_id,
                            last_seen_at,
                            last_seen_ip,
                            last_seen_user_agent,
                        },
                    )
                })
                .collect();

            let update_count = updates.len();

            // [COMMENT]: Encode Protobuf và publish lên NATS
            let req = BulkTouchDevicesRequest { updates };
            let mut buf = Vec::with_capacity(req.encoded_len());
            if let Err(e) = req.encode(&mut buf) {
                Logger::sys_error(
                    "presence.worker",
                    "Failed to encode BulkTouchDevicesRequest",
                    &e.to_string(),
                );
                continue;
            }

            match nats_client
                .publish(NATS_SUBJECT.to_string(), buf.into())
                .await
            {
                Ok(_) => {
                    Logger::sys_info(
                        "presence.worker",
                        &format!(
                            "Published {} device heartbeats to NATS subject '{}'",
                            update_count, NATS_SUBJECT
                        ),
                    );
                }
                Err(e) => {
                    Logger::sys_error(
                        "presence.worker",
                        "Failed to publish heartbeat batch to NATS",
                        &e.to_string(),
                    );
                }
            }
        }
    });
}
