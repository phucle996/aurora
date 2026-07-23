use crate::config::Config;
use crate::infra::kafka::transport_proto::DeadLetterRecordV1;
use crate::infra::kafka::KafkaTransport;
use crate::observability::logger::Logger;
use prost::Message;
use std::collections::HashMap;
use std::sync::Arc;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

/// [COMMENT]: Kafka group phân phối mỗi Zone report cho đúng một JO replica; offset commit sau processor.
pub async fn run_backpressure_listener(
    config: &Config,
    kafka: Arc<KafkaTransport>,
) -> Result<(), Box<dyn std::error::Error>> {
    let topic = kafka.zone_report_topic();
    let consumer = kafka
        .consumer("aurora-job-orchestrator-zone-reports-v1", &topic)
        .await
        .map_err(std::io::Error::other)?;
    let mut zone_heartbeats: HashMap<String, (Instant, String, bool, bool)> = HashMap::new();
    let mut service_metrics_cache: HashMap<(String, String), (String, i32, Instant)> =
        HashMap::new();
    let mut node_heartbeats: HashMap<(String, String), Instant> = HashMap::new();

    match super::super::db::query_all_zone_services_enabled(&config.database_url).await {
        Ok(snapshot) => {
            for (zone_id, services) in &snapshot {
                let (zone_status, _) =
                    super::super::db::query_current_state(&config.database_url, zone_id, "mail")
                        .await
                        .unwrap_or_else(|_| ("active".to_string(), false));
                zone_heartbeats.insert(
                    zone_id.clone(),
                    (
                        Instant::now(),
                        zone_status,
                        services.get("mail").copied().unwrap_or(false),
                        services.get("storage").copied().unwrap_or(false),
                    ),
                );
            }
        }
        Err(error) => Logger::sys_error(
            "zone_report.bootstrap",
            "Bootstrap enabled-services cache failed",
            &error.to_string(),
        ),
    }

    loop {
        let records = consumer.poll(Duration::from_secs(2)).await?;
        for record in records {
            let payload = record.value.unwrap_or_default();
            let key = record.key.unwrap_or_default();
            let zone_id = std::str::from_utf8(&key).unwrap_or_default().to_string();
            let now = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map(|duration| duration.as_secs().min(i64::MAX as u64) as i64)
                .unwrap_or_default();
            let valid_report =
                super::zone_proto::ZoneReport::decode(payload.as_ref()).is_ok_and(|report| {
                    !zone_id.is_empty()
                        && report.zone_id == zone_id
                        && report.timestamp > 0
                        && report.timestamp <= now.saturating_add(300)
                        && report.timestamp >= now.saturating_sub(86_400)
                });
            if !valid_report {
                // [COMMENT]: Scope/timestamp/protobuf sai là poison data, không phải transient DB error.
                let dlq = DeadLetterRecordV1 {
                    event_id: uuid::Uuid::new_v4().as_bytes().to_vec(),
                    source_topic: record.topic.clone(),
                    source_partition: record.partition,
                    source_offset: record.offset,
                    error_code: "ZONE_REPORT_PROTO_INVALID".to_string(),
                    error_message: "ZoneReport failed key, scope or timestamp validation"
                        .to_string(),
                    original_payload: payload.to_vec(),
                    failed_at_unix_ms: chrono::Utc::now().timestamp_millis(),
                    schema_version: 1,
                };
                let dlq_key = dlq.event_id.clone();
                kafka
                    .publish_message(&kafka.dead_letter_topic(), &dlq_key, &dlq)
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
            super::processor::process_report(
                config,
                zone_id,
                payload.to_vec(),
                &mut zone_heartbeats,
                &mut service_metrics_cache,
                &mut node_heartbeats,
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

        super::deadman::check_zone_heartbeats(
            config,
            &mut zone_heartbeats,
            &mut service_metrics_cache,
        )
        .await;
        super::deadman::check_node_heartbeats(config, &mut node_heartbeats).await;
    }
}
