use futures_util::StreamExt;
use std::sync::Arc;
use std::time::Duration;

use crate::config::Config;
use crate::infra::redis::RedisClientManager;
use crate::infra::zone_kv::ZoneKvStore;
use crate::observability::logger::Logger;

/// [COMMENT]: Metadata reconciliation dùng Redis Job làm request transport; shared snapshot/lock chỉ nằm trong NATS KV.
#[allow(deprecated)]
pub async fn sync_zone_metadata(
    zone_kv: Arc<ZoneKvStore>,
    redis_job: Arc<RedisClientManager>,
    config: Arc<Config>,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let owner_id = format!(
        "{}-{}",
        std::env::var("HOSTNAME").unwrap_or_else(|_| std::process::id().to_string()),
        uuid::Uuid::new_v4()
    );
    let Some(lease) = zone_kv
        .acquire_lease(
            "lease.gateway.metadata_sync",
            &owner_id,
            Duration::from_secs(10),
        )
        .await
        .map_err(std::io::Error::other)?
    else {
        return Ok(());
    };

    let result = async {
        let mut publisher = redis_job
            .client()
            .get_multiplexed_tokio_connection()
            .await?;
        let connection = redis_job.client().get_async_connection().await?;
        let mut pubsub = connection.into_pubsub();
        let request_id = uuid::Uuid::new_v4();
        let reply_channel = format!("zone:reply:metadata:{}:{request_id}", config.zone_id);
        pubsub.subscribe(&reply_channel).await?;
        let request = serde_json::to_vec(&serde_json::json!({
            "zone_id": config.zone_id,
            "reply_channel": reply_channel
        }))?;
        let _: () = redis::cmd("PUBLISH")
            .arg("zone:query:metadata")
            .arg(request)
            .query_async(&mut publisher)
            .await?;
        let mut messages = pubsub.on_message();
        let message = tokio::time::timeout(Duration::from_secs(5), messages.next())
            .await
            .map_err(|_| {
                std::io::Error::new(
                    std::io::ErrorKind::TimedOut,
                    "zone metadata response timeout",
                )
            })?
            .ok_or_else(|| {
                std::io::Error::new(
                    std::io::ErrorKind::UnexpectedEof,
                    "zone metadata response closed",
                )
            })?;
        let payload: Vec<u8> = message.get_payload()?;
        let response: serde_json::Value = serde_json::from_slice(&payload)?;
        let status = response
            .get("status")
            .and_then(serde_json::Value::as_str)
            .unwrap_or("inactive");
        zone_kv
            .update_zone_metadata(Some(status), None)
            .await
            .map_err(std::io::Error::other)?;
        if let Some(services) = response
            .get("services")
            .and_then(serde_json::Value::as_object)
        {
            for (name, enabled) in services {
                zone_kv
                    .update_zone_metadata(None, Some((name, enabled.as_bool().unwrap_or(false))))
                    .await
                    .map_err(std::io::Error::other)?;
            }
        }
        Logger::sys_info(
            "zone_gateway.sync_metadata",
            &format!("Đã đồng bộ Zone {} vào NATS KV", config.zone_id),
        );
        Ok::<(), Box<dyn std::error::Error + Send + Sync>>(())
    }
    .await;
    let _ = zone_kv.release_lease(&lease).await;
    result
}
