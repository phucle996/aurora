use futures_util::StreamExt;
use std::sync::Arc;
use std::time::Duration;
use tokio::time::sleep;

use crate::config::Config;
use crate::infra::redis::RedisClientManager;
use crate::observability::logger::Logger;

/// [COMMENT]: Lắng nghe các sự kiện cập nhật cấu hình thời gian thực (CDC events) từ Platform L1 PubSub
#[allow(deprecated)]
pub fn start_metadata_event_listener(
    redis_internal_zone: Arc<RedisClientManager>,
    redis_job: Arc<RedisClientManager>,
    config: Arc<Config>,
) {
    tokio::spawn(async move {
        let channel_name = format!("zone:event:metadata:{}", config.zone_id);
        Logger::sys_info(
            "zone_gateway.cdc_listener",
            &format!(
                "Bắt đầu lắng nghe sự kiện CDC Metadata thời gian thực trên kênh {}",
                channel_name
            ),
        );

        loop {
            // [COMMENT]: Tự động hồi phục kết nối nếu PubSub bị đứt
            let conn_res = redis_job.client().get_async_connection().await;
            match conn_res {
                Ok(conn) => {
                    #[allow(deprecated)]
                    let mut pubsub = conn.into_pubsub();
                    if let Err(e) = pubsub.subscribe(&channel_name).await {
                        Logger::sys_error(
                            "zone_gateway.cdc_listener_error",
                            "Không thể subscribe kênh sự kiện CDC",
                            &e.to_string(),
                        );
                        sleep(Duration::from_secs(5)).await;
                        continue;
                    }

                    let mut stream = pubsub.on_message();
                    let mut conn_l2_opt = None;

                    while let Some(msg) = stream.next().await {
                        // [COMMENT]: Đảm bảo có kết nối Redis L2 để ghi nhận sự thay đổi cấu hình
                        if conn_l2_opt.is_none() {
                            if let Ok(conn) = redis_internal_zone
                                .client()
                                .get_multiplexed_tokio_connection()
                                .await
                            {
                                conn_l2_opt = Some(conn);
                            }
                        }

                        if let Some(mut conn_l2) = conn_l2_opt.clone() {
                            let payload_bin: Vec<u8> = msg.get_payload().unwrap_or_default();
                            if let Ok(event_json) =
                                serde_json::from_slice::<serde_json::Value>(&payload_bin)
                            {
                                let event_type = event_json
                                    .get("event_type")
                                    .and_then(|v| v.as_str())
                                    .unwrap_or("");

                                match event_type {
                                    "zone_status_changed" => {
                                        if let Some(status) =
                                            event_json.get("status").and_then(|v| v.as_str())
                                        {
                                            let _: Result<(), redis::RedisError> =
                                                redis::cmd("HSET")
                                                    .arg("infra:zone:metadata")
                                                    .arg("status")
                                                    .arg(status)
                                                    .query_async(&mut conn_l2)
                                                    .await;

                                            Logger::sys_info(
                                                "zone_gateway.cdc_event",
                                                &format!("[CDC EVENT] Đã cập nhật trạng thái Zone sang: '{}'", status),
                                            );
                                        }
                                    }
                                    "service_status_changed" => {
                                        let service = event_json
                                            .get("service")
                                            .and_then(|v| v.as_str())
                                            .unwrap_or("");
                                        let enabled = event_json
                                            .get("enabled")
                                            .and_then(|v| v.as_bool())
                                            .unwrap_or(false);

                                        if !service.is_empty() {
                                            let field_name = format!("service:{}", service);
                                            let val_str =
                                                if enabled { "enabled" } else { "disabled" };
                                            let _: Result<(), redis::RedisError> =
                                                redis::cmd("HSET")
                                                    .arg("infra:zone:metadata")
                                                    .arg(&field_name)
                                                    .arg(val_str)
                                                    .query_async(&mut conn_l2)
                                                    .await;

                                            Logger::sys_info(
                                                "zone_gateway.cdc_event",
                                                &format!("[CDC EVENT] Đã cập nhật trạng thái dịch vụ '{}' sang: '{}'", service, val_str),
                                            );
                                        }
                                    }
                                    _ => {}
                                }
                            }
                        }
                    }
                }
                Err(e) => {
                    Logger::sys_error(
                        "zone_gateway.cdc_listener_reconnect",
                        "Mất kết nối tới Platform L1 Redis. Thử kết nối lại sau 5s...",
                        &e.to_string(),
                    );
                    sleep(Duration::from_secs(5)).await;
                }
            }
        }
    });
}
