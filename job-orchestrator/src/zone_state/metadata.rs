use crate::config::Config;
use crate::infra::kafka::transport_proto::{
    DeadLetterRecordV1, ZoneMetadataQueryV1, ZoneMetadataSnapshotV1, ZoneServiceDesiredStateV1,
};
use crate::infra::kafka::KafkaTransport;
use prost::Message;
use std::sync::Arc;
use std::time::Duration;

/// [COMMENT]: Query consumer thay Redis PubSub reply-channel; response vào compacted per-Zone topic.
pub async fn run_metadata_query_listener(
    config: &Config,
    kafka: Arc<KafkaTransport>,
) -> Result<(), Box<dyn std::error::Error>> {
    let pg_client =
        crate::infra::postgres::connect(&config.postgres, "zone_metadata.postgres").await?;
    pg_client
        .batch_execute("SET default_transaction_read_only = on")
        .await?;
    let topic = kafka.metadata_query_topic();
    let consumer = kafka
        .consumer("aurora-job-orchestrator-zone-metadata-query-v1", &topic)
        .await
        .map_err(std::io::Error::other)?;
    loop {
        let records = consumer.poll(Duration::from_secs(1)).await?;
        for record in records {
            let payload = record.value.unwrap_or_default();
            let query = match (payload.len() <= 64 * 1024)
                .then(|| ZoneMetadataQueryV1::decode(payload.as_ref()))
            {
                Some(Ok(query))
                    if query.schema_version == 1
                        && query.request_id.len() == 16
                        && query.zone_id.len() == 16
                        && query.requested_at_unix_ms > 0 =>
                {
                    query
                }
                _ => {
                    let dlq = DeadLetterRecordV1 {
                        event_id: uuid::Uuid::new_v4().as_bytes().to_vec(),
                        source_topic: record.topic.clone(),
                        source_partition: record.partition,
                        source_offset: record.offset,
                        error_code: "ZONE_METADATA_QUERY_PROTO_INVALID".to_string(),
                        error_message: "ZoneMetadataQueryV1 failed strict validation".to_string(),
                        original_payload: payload.iter().take(4_096).copied().collect(),
                        failed_at_unix_ms: chrono::Utc::now().timestamp_millis(),
                        schema_version: 1,
                    };
                    let key = dlq.event_id.clone();
                    kafka
                        .publish_message(&kafka.dead_letter_topic(), &key, &dlq)
                        .await
                        .map_err(std::io::Error::other)?;
                    kafka
                        .commit(
                            &consumer,
                            &record.topic,
                            record.partition,
                            record.offset + 1,
                        )
                        .await
                        .map_err(std::io::Error::other)?;
                    continue;
                }
            };
            let zone_id = uuid::Uuid::from_slice(&query.zone_id)?.to_string();
            let (status, services) =
                super::store::query_zone_metadata(&pg_client, &zone_id).await?;
            let snapshot = ZoneMetadataSnapshotV1 {
                event_id: query.request_id,
                zone_id: query.zone_id,
                status,
                services: services
                    .into_iter()
                    .map(|(service_type, enabled)| ZoneServiceDesiredStateV1 {
                        service_type,
                        enabled,
                    })
                    .collect(),
                observed_at_unix_ms: chrono::Utc::now().timestamp_millis(),
                schema_version: 1,
            };
            kafka
                .publish_message(
                    &kafka.metadata_topic(&zone_id),
                    zone_id.as_bytes(),
                    &snapshot,
                )
                .await
                .map_err(std::io::Error::other)?;
            kafka
                .commit(
                    &consumer,
                    &record.topic,
                    record.partition,
                    record.offset + 1,
                )
                .await
                .map_err(std::io::Error::other)?;
        }
    }
}
