use futures_util::StreamExt;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use crate::config::Config;
use crate::infra::redis::RedisClientManager;
use crate::observability::logger::Logger;

/// [COMMENT]: Đồng bộ metadata của Zone (Status & Services) từ Platform L1 về Redis L2 cục bộ (Reverse Metadata Query Flow)
#[allow(deprecated)]
pub async fn sync_zone_metadata(
    redis_internal_zone: Arc<RedisClientManager>,
    redis_job: Arc<RedisClientManager>,
    config: Arc<Config>,
) -> Result<(), Box<dyn std::error::Error>> {
    let mut conn_l2 = redis_internal_zone
        .client()
        .get_multiplexed_tokio_connection()
        .await?;

    // [COMMENT]: 0. Cố gắng lấy Distributed Lock trên Redis L2 để tránh Write Stampede/Double-query
    let node_id = uuid::Uuid::new_v4().to_string();
    let lock_key = "lock:zone:sync_metadata";

    let acquired: Result<Option<String>, redis::RedisError> = redis::cmd("SET")
        .arg(lock_key)
        .arg(&node_id)
        .arg("NX")
        .arg("EX")
        .arg(10) // Lock TTL 10 giây
        .query_async(&mut conn_l2)
        .await;

    let has_lock = match acquired {
        Ok(Some(s)) if s == "OK" => true,
        _ => false,
    };

    if !has_lock {
        // [COMMENT]: Thất bại trong việc lấy lock -> Đã có node khác đang chạy sync
        Logger::sys_debug(
            "zone_gateway.sync_metadata",
            "Không thể lấy lock đồng bộ (đã có node khác trong cụm đang chạy). Bỏ qua chu kỳ sync metadata.",
        );
        return Ok(());
    }

    Logger::sys_info(
        "zone_gateway.sync_metadata",
        "Đã lấy thành công lock đồng bộ metadata. Tiến hành sync từ Platform L1...",
    );

    let mut conn_l1 = redis_job
        .client()
        .get_multiplexed_tokio_connection()
        .await?;
    let conn_l1_async = redis_job.client().get_async_connection().await?;
    let mut pubsub_conn = conn_l1_async.into_pubsub();

    let request_uuid = uuid::Uuid::new_v4().to_string();
    let reply_channel = format!("zone:reply:metadata:{}:{}", config.zone_id, request_uuid);

    // [COMMENT]: 1. Subscribe kênh reply nhận kết quả phản hồi từ job-proxy
    pubsub_conn.subscribe(&reply_channel).await?;

    let wait_res = {
        let mut stream = pubsub_conn.on_message();

        // [COMMENT]: 2. Publish request hỏi metadata lên kênh Platform dưới dạng nhị phân thô
        let req_payload = serde_json::json!({
            "zone_id": config.zone_id,
            "reply_channel": reply_channel
        });

        let req_bin = serde_json::to_vec(&req_payload).unwrap_or_default();

        let _: Result<(), redis::RedisError> = redis::cmd("PUBLISH")
            .arg("zone:query:metadata")
            .arg(&req_bin[..])
            .query_async(&mut conn_l1)
            .await;

        // [COMMENT]: 3. Đợi nhận phản hồi trong timeout 5 giây (HA Safety & Non-blocking fallback)
        tokio::time::timeout(Duration::from_secs(5), stream.next()).await
    };

    // [COMMENT]: Luôn luôn hủy subscribe kênh reply sau khi xong để tránh rò rỉ bộ nhớ
    let _ = pubsub_conn.unsubscribe(&reply_channel).await;

    match wait_res {
        Ok(Some(msg)) => {
            let payload_bin: Vec<u8> = msg.get_payload().unwrap_or_default();
            let resp_json: serde_json::Value = serde_json::from_slice(&payload_bin)?;

            let status = resp_json
                .get("status")
                .and_then(|v| v.as_str())
                .unwrap_or("inactive");
            let services = resp_json.get("services").and_then(|v| v.as_object());

            let mut pipe = redis::pipe();
            pipe.cmd("HSET")
                .arg("infra:zone:metadata")
                .arg("status")
                .arg(status);

            if let Some(svcs) = services {
                for (svc_name, enabled_val) in svcs {
                    let val_str = if enabled_val.as_bool().unwrap_or(false) {
                        "enabled"
                    } else {
                        "disabled"
                    };
                    pipe.cmd("HSET")
                        .arg("infra:zone:metadata")
                        .arg(format!("service:{}", svc_name))
                        .arg(val_str);
                }
            }

            pipe.cmd("HSET")
                .arg("infra:zone:metadata")
                .arg("updated_at")
                .arg(
                    SystemTime::now()
                        .duration_since(UNIX_EPOCH)
                        .unwrap_or_default()
                        .as_secs(),
                );

            let _: () = pipe.query_async(&mut conn_l2).await?;

            Logger::sys_info(
                "zone_gateway.sync_metadata",
                &format!(
                    "Đã đồng bộ metadata Zone {} (Status: {}) về Redis L2 thành công.",
                    config.zone_id, status
                ),
            );
        }
        _ => {
            Logger::sys_error(
                "zone_gateway.sync_metadata_timeout",
                "Quá hạn 5 giây không nhận được metadata phản hồi từ Platform",
                "Timeout or Empty response",
            );
        }
    }

    // [COMMENT]: 4. Giải phóng Distributed Lock nguyên tử sử dụng Lua script (HA Safety)
    let lua_script = "
        if redis.call('get', KEYS[1]) == ARGV[1] then
            return redis.call('del', KEYS[1])
        else
            return 0
        end
    ";
    let _: Result<i32, redis::RedisError> = redis::cmd("EVAL")
        .arg(lua_script)
        .arg(1)
        .arg(lock_key)
        .arg(&node_id)
        .query_async(&mut conn_l2)
        .await;

    Ok(())
}
