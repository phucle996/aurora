use crate::config::Config;
use crate::infra::kafka::transport_proto::DeadLetterRecordV1;
use crate::infra::kafka::KafkaTransport;
use prost::Message;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

/// [COMMENT]: Kafka group phân phối mỗi Zone report cho đúng một JO replica; offset commit sau processor.
pub async fn run_backpressure_listener(
    config: &Config,
    kafka: Arc<KafkaTransport>,
) -> Result<(), Box<dyn std::error::Error>> {
    let pg_client =
        crate::infra::postgres::connect(&config.postgres, "zone_state.postgres").await?;
    let topic = kafka.zone_report_topic();
    let consumer = kafka
        .consumer("aurora-job-orchestrator-zone-reports-v1", &topic)
        .await
        .map_err(std::io::Error::other)?;
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
            let report = (payload.len() <= 1024 * 1024)
                .then(|| super::proto::ZoneReport::decode(payload.as_ref()).ok())
                .flatten();
            let valid_report = report.as_ref().is_some_and(|report| {
                let cluster = report.dataplane_cluster.as_ref();
                let workloads = report.workloads.as_ref();
                !zone_id.is_empty()
                    && uuid::Uuid::parse_str(&zone_id).is_ok()
                    && report.zone_id == zone_id
                    && report.timestamp > 0
                    && report.timestamp <= now.saturating_add(300)
                    && report.timestamp >= now.saturating_sub(86_400)
                    && cluster.is_some_and(|cluster| {
                        cluster.avg_cpu_usage.is_finite()
                            && (0.0..=1.0).contains(&cluster.avg_cpu_usage)
                            && cluster.avg_ram_usage.is_finite()
                            && (0.0..=1.0).contains(&cluster.avg_ram_usage)
                            && cluster.job_queue_lag >= 0
                            && cluster.active_nodes >= 0
                            && cluster.total_active_workers >= 0
                            && cluster.total_max_workers >= cluster.total_active_workers
                    })
                    && workloads.is_some_and(|workloads| {
                        let valid_service = |status: &str, capacity: i32| {
                            matches!(status, "healthy" | "degraded" | "down")
                                && (0..=100).contains(&capacity)
                        };
                        workloads
                            .mail
                            .as_ref()
                            .is_some_and(|mail| valid_service(&mail.status, mail.capacity))
                            && workloads.storage.as_ref().is_some_and(|storage| {
                                valid_service(&storage.status, storage.capacity)
                            })
                            && workloads.hypervisors.len() <= 4_096
                            && workloads.hypervisors.iter().all(|node| {
                                !node.node_code.is_empty()
                                    && node.node_code.len() <= 128
                                    && node.status.len() <= 32
                                    && node.cpu_cores_total >= 0
                                    && node.cpu_cores_used >= 0
                                    && node.cpu_cores_used <= node.cpu_cores_total
                                    && node.ram_mb_total >= 0
                                    && node.ram_mb_used >= 0
                                    && node.ram_mb_used <= node.ram_mb_total
                                    && node.storage_gb_total >= 0
                                    && node.storage_gb_used >= 0
                                    && node.storage_gb_used <= node.storage_gb_total
                            })
                    })
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
                    // Poison telemetry can be large or contain provider details.
                    // Keep only a bounded prefix for diagnosis.
                    original_payload: payload.iter().take(4_096).copied().collect(),
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
                &pg_client,
                zone_id,
                report.expect("validated report"),
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
