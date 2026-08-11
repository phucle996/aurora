use std::sync::Arc;
use std::time::Duration;

use crate::config::Config;
use crate::contracts::storage_usage_report::StorageUsageReportV1;
use crate::infra::kafka::{transport_proto::DeadLetterRecordV1, KafkaTransport};
use crate::observability::logger::Logger;
use prost::Message;
use sha2::{Digest, Sha256};

const USAGE_REPORT_STREAM: &str = "aurora:storage:usage:reports";
const USAGE_REPORT_GROUP: &str = "cost-engine-storage-metering-v1";
const MAX_REPORT_BYTES: usize = 4 * 1024 * 1024;
const MAX_AGGREGATES: usize = 100_000;

pub async fn run_usage_report_relay(
    config: &Config,
    kafka: Arc<KafkaTransport>,
    redis_client: &redis::Client,
) -> Result<(), Box<dyn std::error::Error>> {
    let mut redis_conn =
        crate::infra::redis::multiplexed(redis_client, &config.shared_redis).await?;
    let _: redis::RedisResult<()> = redis::cmd("XGROUP")
        .arg("CREATE")
        .arg(USAGE_REPORT_STREAM)
        .arg(USAGE_REPORT_GROUP)
        .arg("0-0")
        .arg("MKSTREAM")
        .query_async(&mut redis_conn)
        .await;
    let topic = kafka.storage_usage_reports_topic();
    let consumer = kafka
        .consumer("aurora-job-orchestrator-storage-usage-v1", &topic)
        .await
        .map_err(std::io::Error::other)?;

    loop {
        for record in consumer.poll(Duration::from_secs(1)).await? {
            let payload = record.value.unwrap_or_default();
            let report = match decode_report(&payload) {
                Ok(report) => report,
                Err(error_code) => {
                    let dlq = DeadLetterRecordV1 {
                        event_id: uuid::Uuid::new_v4().as_bytes().to_vec(),
                        source_topic: record.topic.clone(),
                        source_partition: record.partition,
                        source_offset: record.offset,
                        error_code: error_code.to_string(),
                        error_message: "StorageUsageReportV1 failed strict validation".to_string(),
                        // Storage reports are not copied into the DLQ. A
                        // malformed payload is attacker-controlled and may
                        // contain credentials or object names.
                        original_payload: Vec::new(),
                        failed_at_unix_ms: chrono::Utc::now().timestamp_millis(),
                        schema_version: 1,
                    };
                    kafka
                        .publish_message(&kafka.dead_letter_topic(), &dlq.event_id, &dlq)
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
            let report_id = report.report_id.clone();
            let zone_id = report.zone_id.clone();
            let checksum = report.report_sha256.clone();
            let _: String = redis::cmd("XADD")
                .arg(USAGE_REPORT_STREAM)
                .arg("*")
                .arg("report_id")
                .arg(&report_id)
                .arg("zone_id")
                .arg(&zone_id)
                .arg("report_sha256")
                .arg(checksum.as_slice())
                .arg("payload")
                .arg(payload.as_ref())
                .query_async(&mut redis_conn)
                .await?;
            kafka
                .commit(
                    &consumer,
                    &record.topic,
                    record.partition,
                    record.offset + 1,
                )
                .await
                .map_err(std::io::Error::other)?;
            Logger::sys_info(
                "storage_metering.relay",
                "Storage usage report durably relayed to Shared Redis Stream",
            );
        }
    }
}

fn decode_report(payload: &[u8]) -> Result<StorageUsageReportV1, &'static str> {
    if payload.is_empty() || payload.len() > MAX_REPORT_BYTES {
        return Err("STORAGE_USAGE_REPORT_SIZE_INVALID");
    }
    let report =
        StorageUsageReportV1::decode(payload).map_err(|_| "STORAGE_USAGE_REPORT_PROTO_INVALID")?;
    let report_id =
        uuid::Uuid::parse_str(&report.report_id).map_err(|_| "STORAGE_USAGE_REPORT_ID_INVALID")?;
    let zone_id =
        uuid::Uuid::parse_str(&report.zone_id).map_err(|_| "STORAGE_USAGE_REPORT_ZONE_INVALID")?;
    if report.schema_version != 1
        || report_id.is_nil()
        || zone_id.is_nil()
        || report.window_end_unix_ms <= report.window_start_unix_ms
        || report.window_end_unix_ms - report.window_start_unix_ms > 86_400_000
        || report.aggregates.is_empty()
        || report.aggregates.len() > MAX_AGGREGATES
        || report.report_sha256.len() != 32
    {
        return Err("STORAGE_USAGE_REPORT_CONTRACT_INVALID");
    }
    if report.aggregates.iter().any(|aggregate| {
        uuid::Uuid::parse_str(&aggregate.resource_id).is_err() || aggregate.resource_id.is_empty()
    }) {
        return Err("STORAGE_USAGE_REPORT_RESOURCE_INVALID");
    }
    if report.correction {
        let correction_of = uuid::Uuid::parse_str(&report.correction_of_report_id)
            .map_err(|_| "STORAGE_USAGE_REPORT_CORRECTION_INVALID")?;
        if correction_of.is_nil() || correction_of == report_id {
            return Err("STORAGE_USAGE_REPORT_CORRECTION_INVALID");
        }
    } else if !report.correction_of_report_id.is_empty() {
        return Err("STORAGE_USAGE_REPORT_CORRECTION_INVALID");
    }

    let mut canonical = report.clone();
    canonical.report_sha256.clear();
    let encoded = canonical.encode_to_vec();
    let digest = Sha256::digest(encoded);
    if report.report_sha256.as_slice() != digest.as_slice() {
        return Err("STORAGE_USAGE_REPORT_CHECKSUM_INVALID");
    }
    Ok(report)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn valid_report() -> StorageUsageReportV1 {
        let mut report = StorageUsageReportV1 {
            schema_version: 1,
            report_id: uuid::Uuid::new_v4().to_string(),
            zone_id: uuid::Uuid::new_v4().to_string(),
            window_start_unix_ms: 1_000,
            window_end_unix_ms: 2_000,
            sequence: 1,
            correction: false,
            aggregates: vec![
                crate::contracts::storage_usage_report::StorageUsageAggregateV1 {
                    resource_id: uuid::Uuid::new_v4().to_string(),
                    upload_bytes: 0,
                    download_bytes: 42,
                    request_count: 1,
                },
            ],
            report_sha256: Vec::new(),
            correction_of_report_id: String::new(),
        };
        let digest = Sha256::digest(report.encode_to_vec());
        report.report_sha256 = digest.to_vec();
        report
    }

    #[test]
    fn accepts_report_with_canonical_checksum() {
        let report = valid_report();
        assert!(decode_report(&report.encode_to_vec()).is_ok());
    }

    #[test]
    fn rejects_checksum_tampering() {
        let mut report = valid_report();
        report.aggregates[0].download_bytes = 43;
        assert_eq!(
            decode_report(&report.encode_to_vec()),
            Err("STORAGE_USAGE_REPORT_CHECKSUM_INVALID")
        );
    }

    #[test]
    fn rejects_non_correction_with_parent() {
        let mut report = valid_report();
        report.correction_of_report_id = uuid::Uuid::new_v4().to_string();
        let digest = Sha256::digest({
            let mut canonical = report.clone();
            canonical.report_sha256.clear();
            canonical.encode_to_vec()
        });
        report.report_sha256 = digest.to_vec();
        assert_eq!(
            decode_report(&report.encode_to_vec()),
            Err("STORAGE_USAGE_REPORT_CORRECTION_INVALID")
        );
    }
}
