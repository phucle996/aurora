use crate::config::Config;
use crate::observability::logger::Logger;
use futures_util::StreamExt;

/// [COMMENT]: Lắng nghe các yêu cầu truy vấn metadata ngược từ Dataplane qua PubSub (Platform Level Metadata Query)
/// Dataplane gửi request lên kênh `zone:query:metadata` với reply_channel.
/// Job Orchestrator query DB SoT và phản hồi binary JSON về kênh reply tương ứng.
#[allow(deprecated)]
pub async fn run_metadata_query_listener(
    config: &Config,
    redis_client: &redis::Client,
) -> Result<(), Box<dyn std::error::Error>> {
    // [COMMENT]: Dùng multiplexed conn để publish reply không blocking pubsub stream
    let conn = redis_client.get_multiplexed_tokio_connection().await?;
    let conn_async = redis_client.get_async_connection().await?;
    let mut pubsub_conn = conn_async.into_pubsub();

    let channel_name = "zone:query:metadata";
    pubsub_conn.subscribe(channel_name).await?;

    Logger::sys_info(
        "metadata_query_listener.run",
        &format!(
            "MetadataQueryListener: Đang lắng nghe kênh PubSub '{}'...",
            channel_name
        ),
    );

    let mut stream = pubsub_conn.on_message();

    while let Some(msg) = stream.next().await {
        let payload_bin: Vec<u8> = msg.get_payload().unwrap_or_default();
        if payload_bin.is_empty() {
            continue;
        }

        // [COMMENT]: Parse request JSON thô dạng binary (Avoid UTF-8 String allocations)
        let req_json: serde_json::Value = match serde_json::from_slice(&payload_bin) {
            Ok(v) => v,
            Err(_) => continue,
        };

        let zone_id = match req_json.get("zone_id").and_then(|v| v.as_str()) {
            Some(z) => z.to_string(),
            None => continue,
        };

        let reply_channel = match req_json.get("reply_channel").and_then(|v| v.as_str()) {
            Some(rc) => rc.to_string(),
            None => continue,
        };

        // [COMMENT]: Spawn task riêng để query DB và reply, tránh block stream PubSub
        let db_url = config.database_url.clone();
        let zone_id_clone = zone_id.clone();
        let reply_channel_clone = reply_channel.clone();
        let mut conn_clone = conn.clone();

        tokio::spawn(async move {
            match super::super::db::query_zone_metadata(&db_url, &zone_id_clone)
                .await
                .map_err(|e| e.to_string())
            {
                Ok((status, services)) => {
                    let response = serde_json::json!({
                        "zone_id": zone_id_clone,
                        "status": status,
                        "services": services
                    });

                    // [COMMENT]: Mã hóa thẳng sang Vec<u8> để truyền tải nhị phân (Network Optimize)
                    if let Ok(response_bin) = serde_json::to_vec(&response) {
                        let publish_res: Result<(), redis::RedisError> = redis::cmd("PUBLISH")
                            .arg(&reply_channel_clone)
                            .arg(&response_bin[..])
                            .query_async(&mut conn_clone)
                            .await;

                        if let Err(e) = publish_res {
                            Logger::sys_error(
                                "metadata_query_listener.publish",
                                "Thất bại khi gửi metadata phản hồi về Dataplane",
                                &e.to_string(),
                            );
                        }
                    }
                }
                Err(e) => {
                    Logger::sys_error(
                        "metadata_query_listener.db_query",
                        &format!("Lỗi khi truy vấn metadata cho Zone {}", zone_id_clone),
                        &e.to_string(),
                    );
                }
            }
        });
    }

    Ok(())
}
