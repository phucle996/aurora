use std::collections::HashSet;
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
const MAX_REPORT_BYTES: usize = 512 * 1024;
const MAX_AGGREGATES: usize = 10_000;
const HOURLY_WINDOW_MS: i64 = 3_600_000;
const MAX_CLOCK_SKEW_MS: i64 = 5 * 60 * 1_000;
const MAX_REPORT_AGE_MS: i64 = 30 * 86_400_000;
const REPORT_NAMESPACE: uuid::Uuid =
    uuid::Uuid::from_u128(0x5f0a_8e90_46e5_4fbb_8c01_7108_7f8c_1f22);

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
        || report.window_end_unix_ms - report.window_start_unix_ms != HOURLY_WINDOW_MS
        || report.window_start_unix_ms.rem_euclid(HOURLY_WINDOW_MS) != 0
        || report.aggregates.is_empty()
        || report.aggregates.len() > MAX_AGGREGATES
        || report.report_sha256.len() != 32
    {
        return Err("STORAGE_USAGE_REPORT_CONTRACT_INVALID");
    }
    let now = chrono::Utc::now().timestamp_millis();
    if report.window_end_unix_ms > now.saturating_add(MAX_CLOCK_SKEW_MS)
        || report.window_start_unix_ms < now.saturating_sub(MAX_REPORT_AGE_MS)
    {
        return Err("STORAGE_USAGE_REPORT_TIME_INVALID");
    }
    let expected_sequence = u64::try_from(report.window_end_unix_ms.div_euclid(HOURLY_WINDOW_MS))
        .map_err(|_| "STORAGE_USAGE_REPORT_SEQUENCE_INVALID")?;
    if !report.correction {
        let expected_report_id = uuid::Uuid::new_v5(
            &REPORT_NAMESPACE,
            format!(
                "{}:{}:{}:{}",
                zone_id, report.window_start_unix_ms, report.window_end_unix_ms, expected_sequence
            )
            .as_bytes(),
        );
        if report.sequence != expected_sequence || report_id != expected_report_id {
            return Err("STORAGE_USAGE_REPORT_SEQUENCE_INVALID");
        }
    }
    let mut resources = HashSet::with_capacity(report.aggregates.len());
    let mut last_identity: Option<String> = None;
    if report.aggregates.iter().any(|aggregate| {
        let valid_id = !aggregate.resource_id.is_empty()
            && uuid::Uuid::parse_str(&aggregate.resource_id)
                .is_ok_and(|resource_id| !resource_id.is_nil());
        let valid_name = !aggregate.resource_name.is_empty()
            && aggregate.resource_name.len() <= 255
            && (aggregate.resource_name.starts_with("ws-")
                || aggregate.resource_name.starts_with("tn-"));
        let capacity = aggregate.storage_byte_hours > 0;
        let transfer = aggregate.upload_bytes > 0 || aggregate.download_bytes > 0;
        let identity = if capacity {
            format!("name:{}", aggregate.resource_name)
        } else {
            format!("id:{}", aggregate.resource_id)
        };
        let out_of_order = last_identity
            .as_ref()
            .is_some_and(|previous| identity.as_str() <= previous.as_str());
        last_identity = Some(identity.clone());
        out_of_order
            || (!resources.insert(identity))
            || (capacity
                && (!valid_name
                    || !aggregate.resource_id.is_empty()
                    || transfer
                    || aggregate.request_count != 0
                    || aggregate.storage_bytes != aggregate.storage_byte_hours))
            || (transfer
                && (!valid_id
                    || !aggregate.resource_name.is_empty()
                    || aggregate.request_count == 0
                    || aggregate.storage_bytes != 0
                    || aggregate.storage_byte_hours != 0))
            || (!capacity && !transfer)
            || (aggregate.upload_bytes == 0
                && aggregate.download_bytes == 0
                && aggregate.storage_byte_hours == 0)
            || i64::try_from(aggregate.upload_bytes).is_err()
            || i64::try_from(aggregate.download_bytes).is_err()
            || i64::try_from(aggregate.request_count).is_err()
            || i64::try_from(aggregate.storage_bytes).is_err()
            || i64::try_from(aggregate.storage_byte_hours).is_err()
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
            window_start_unix_ms: chrono::Utc::now()
                .timestamp_millis()
                .div_euclid(HOURLY_WINDOW_MS)
                .saturating_sub(1)
                .saturating_mul(HOURLY_WINDOW_MS),
            window_end_unix_ms: chrono::Utc::now()
                .timestamp_millis()
                .div_euclid(HOURLY_WINDOW_MS)
                .saturating_mul(HOURLY_WINDOW_MS),
            sequence: 0,
            correction: false,
            aggregates: vec![
                crate::contracts::storage_usage_report::StorageUsageAggregateV1 {
                    resource_id: uuid::Uuid::new_v4().to_string(),
                    upload_bytes: 0,
                    download_bytes: 42,
                    request_count: 1,
                    resource_name: String::new(),
                    storage_bytes: 0,
                    storage_byte_hours: 0,
                },
            ],
            report_sha256: Vec::new(),
            correction_of_report_id: String::new(),
        };
        report.sequence =
            u64::try_from(report.window_end_unix_ms.div_euclid(HOURLY_WINDOW_MS)).unwrap();
        report.report_id = uuid::Uuid::new_v5(
            &REPORT_NAMESPACE,
            format!(
                "{}:{}:{}:{}",
                report.zone_id,
                report.window_start_unix_ms,
                report.window_end_unix_ms,
                report.sequence
            )
            .as_bytes(),
        )
        .to_string();
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

    #[test]
    fn rejects_non_hourly_or_forged_initial_report_identity() {
        let mut non_hourly = valid_report();
        non_hourly.window_start_unix_ms += 1;
        assert_eq!(
            decode_report(&non_hourly.encode_to_vec()),
            Err("STORAGE_USAGE_REPORT_CONTRACT_INVALID")
        );

        let mut forged = valid_report();
        forged.report_id = uuid::Uuid::new_v4().to_string();
        assert_eq!(
            decode_report(&forged.encode_to_vec()),
            Err("STORAGE_USAGE_REPORT_SEQUENCE_INVALID")
        );
    }

    #[test]
    fn rejects_capacity_that_was_rounded_before_transport() {
        let mut report = valid_report();
        let aggregate = &mut report.aggregates[0];
        aggregate.resource_id.clear();
        aggregate.resource_name = "ws-capacity".to_string();
        aggregate.upload_bytes = 0;
        aggregate.download_bytes = 0;
        aggregate.request_count = 0;
        aggregate.storage_bytes = 1_001;
        aggregate.storage_byte_hours = 1;
        assert_eq!(
            decode_report(&report.encode_to_vec()),
            Err("STORAGE_USAGE_REPORT_RESOURCE_INVALID")
        );
    }
}
