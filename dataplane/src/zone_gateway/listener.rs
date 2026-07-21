use futures_util::StreamExt;
use std::sync::Arc;
use std::time::Duration;

use crate::config::Config;
use crate::infra::redis::RedisClientManager;
use crate::infra::zone_kv::ZoneKvStore;
use crate::observability::logger::Logger;

/// [COMMENT]: Redis Job PubSub chỉ là transport; event hợp lệ được merge vào durable Zone metadata bằng KV CAS.
#[allow(deprecated)]
pub fn start_metadata_event_listener(
    zone_kv: Arc<ZoneKvStore>,
    redis_job: Arc<RedisClientManager>,
    config: Arc<Config>,
) {
    tokio::spawn(async move {
        let channel = format!("zone:event:metadata:{}", config.zone_id);
        loop {
            match redis_job.client().get_async_connection().await {
                Ok(connection) => {
                    let mut pubsub = connection.into_pubsub();
                    if let Err(error) = pubsub.subscribe(&channel).await {
                        Logger::sys_error(
                            "zone_gateway.cdc_listener",
                            "Không thể subscribe metadata event",
                            &error.to_string(),
                        );
                        tokio::time::sleep(Duration::from_secs(5)).await;
                        continue;
                    }
                    let mut messages = pubsub.on_message();
                    while let Some(message) = messages.next().await {
                        let payload: Vec<u8> = message.get_payload().unwrap_or_default();
                        let Ok(event) = serde_json::from_slice::<serde_json::Value>(&payload)
                        else {
                            continue;
                        };
                        let result = match event
                            .get("event_type")
                            .and_then(serde_json::Value::as_str)
                            .unwrap_or_default()
                        {
                            "zone_status_changed" => {
                                let status = event
                                    .get("status")
                                    .and_then(serde_json::Value::as_str)
                                    .unwrap_or("inactive");
                                zone_kv.update_zone_metadata(Some(status), None).await
                            }
                            "service_status_changed" => {
                                let service = event
                                    .get("service")
                                    .and_then(serde_json::Value::as_str)
                                    .unwrap_or_default();
                                if service.is_empty() {
                                    continue;
                                }
                                let enabled = event
                                    .get("enabled")
                                    .and_then(serde_json::Value::as_bool)
                                    .unwrap_or(false);
                                zone_kv
                                    .update_zone_metadata(None, Some((service, enabled)))
                                    .await
                            }
                            _ => continue,
                        };
                        if let Err(error) = result {
                            Logger::sys_error(
                                "zone_gateway.cdc_listener",
                                "Không thể merge metadata event vào Zone KV",
                                &error,
                            );
                        }
                    }
                }
                Err(error) => {
                    Logger::sys_error(
                        "zone_gateway.cdc_listener",
                        "Mất Redis Job metadata transport",
                        &error.to_string(),
                    );
                    tokio::time::sleep(Duration::from_secs(5)).await;
                }
            }
        }
    });
}
