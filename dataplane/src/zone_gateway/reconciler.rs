use std::sync::Arc;
use std::time::Duration;

use crate::config::Config;
use crate::infra::kafka::transport_proto::ZoneMetadataQueryV1;
use crate::infra::kafka::KafkaTransport;
use crate::infra::zone_kv::ZoneKvStore;

/// [COMMENT]: Query là durable Kafka command; JO trả full snapshot vào compacted per-Zone topic.
pub async fn sync_zone_metadata(
    zone_kv: Arc<ZoneKvStore>,
    kafka: Arc<KafkaTransport>,
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

    let request_id = uuid::Uuid::new_v4();
    let query = ZoneMetadataQueryV1 {
        request_id: request_id.as_bytes().to_vec(),
        zone_id: uuid::Uuid::parse_str(&config.zone_id)
            .map(|value| value.as_bytes().to_vec())
            .unwrap_or_default(),
        requested_at_unix_ms: chrono::Utc::now().timestamp_millis(),
        schema_version: 1,
    };
    let result = kafka
        .publish_message(
            &kafka.metadata_query_topic(),
            config.zone_id.as_bytes(),
            &query,
        )
        .await
        .map_err(std::io::Error::other)
        .map_err(Into::into);
    let _ = zone_kv.release_lease(&lease).await;
    result
}
