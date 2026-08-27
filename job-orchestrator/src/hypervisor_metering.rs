use std::sync::Arc;
use std::time::Duration;

use prost::Message;
use sha2::{Digest, Sha256};

use crate::config::Config;
use crate::contracts::hypervisor_network_usage_report::HypervisorNetworkUsageReportV1;
use crate::infra::kafka::{transport_proto::DeadLetterRecordV1, KafkaTransport};

const STREAM: &str = "aurora:hypervisor:network:usage:reports";
const HOURLY_WINDOW_MS: i64 = 3_600_000;
const MAX_CLOCK_SKEW_MS: i64 = 5 * 60 * 1_000;
const MAX_REPORT_AGE_MS: i64 = 30 * 86_400_000;
const MAX_REPORT_BYTES: usize = 64 * 1024;
const STREAM_CAPACITY: i64 = 1_000_000;
const REPORT_NAMESPACE: uuid::Uuid =
    uuid::Uuid::from_u128(0x98a4_181b_0674_5ca5_a3a1_d2ba_fbd5_1921);

pub async fn run_network_usage_report_relay(
    config: &Config,
    kafka: Arc<KafkaTransport>,
    redis_client: &redis::Client,
) -> Result<(), Box<dyn std::error::Error>> {
    let mut redis_conn =
        crate::infra::redis::multiplexed(redis_client, &config.shared_redis).await?;
    let topic = kafka.hypervisor_network_usage_reports_topic();
    let consumer = kafka
        .consumer(
            "aurora-job-orchestrator-hypervisor-network-usage-v1",
            &topic,
        )
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
                        error_message: "HypervisorNetworkUsageReportV1 failed strict validation"
                            .to_string(),
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
            let _: String = redis::Script::new(
                "if redis.call('XLEN',KEYS[1]) >= tonumber(ARGV[1]) then return redis.error_reply('HYPERVISOR_NETWORK_STREAM_CAPACITY_REACHED') end; return redis.call('XADD',KEYS[1],'*','report_id',ARGV[2],'zone_id',ARGV[3],'resource_id',ARGV[4],'report_sha256',ARGV[5],'payload',ARGV[6])",
            )
            .key(STREAM)
            .arg(STREAM_CAPACITY)
            .arg(&report.report_id)
            .arg(&report.zone_id)
            .arg(&report.resource_id)
            .arg(report.report_sha256.as_slice())
            .arg(payload.as_ref())
            .invoke_async(&mut redis_conn)
            .await?;
            let (local_aof, replica_aof): (i64, i64) = redis::cmd("WAITAOF")
                .arg(1)
                .arg(config.shared_redis.aof_replica_acks)
                .arg(config.shared_redis.aof_timeout_ms)
                .query_async(&mut redis_conn)
                .await?;
            if local_aof < 1 || replica_aof < config.shared_redis.aof_replica_acks {
                return Err(format!(
                    "Hypervisor network Redis durability fence not met: local={local_aof}, replicas={replica_aof}, required={}",
                    config.shared_redis.aof_replica_acks
                )
                .into());
            }
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

fn decode_report(payload: &[u8]) -> Result<HypervisorNetworkUsageReportV1, &'static str> {
    if payload.is_empty() || payload.len() > MAX_REPORT_BYTES {
        return Err("HYPERVISOR_NETWORK_REPORT_SIZE_INVALID");
    }
    let report = HypervisorNetworkUsageReportV1::decode(payload)
        .map_err(|_| "HYPERVISOR_NETWORK_REPORT_PROTO_INVALID")?;
    let report_id = uuid::Uuid::parse_str(&report.report_id)
        .map_err(|_| "HYPERVISOR_NETWORK_REPORT_ID_INVALID")?;
    let zone_id = uuid::Uuid::parse_str(&report.zone_id)
        .map_err(|_| "HYPERVISOR_NETWORK_REPORT_ZONE_INVALID")?;
    let resource_id = uuid::Uuid::parse_str(&report.resource_id)
        .map_err(|_| "HYPERVISOR_NETWORK_REPORT_RESOURCE_INVALID")?;
    if report.schema_version != 1
        || report_id.is_nil()
        || zone_id.is_nil()
        || resource_id.is_nil()
        || report.window_end_unix_ms - report.window_start_unix_ms != HOURLY_WINDOW_MS
        || report.window_start_unix_ms.rem_euclid(HOURLY_WINDOW_MS) != 0
        || report.network_in_bytes == 0 && report.network_out_bytes == 0
        || report.report_sha256.len() != 32
        || i64::try_from(report.network_in_bytes).is_err()
        || i64::try_from(report.network_out_bytes).is_err()
    {
        return Err("HYPERVISOR_NETWORK_REPORT_CONTRACT_INVALID");
    }
    let now = chrono::Utc::now().timestamp_millis();
    if report.window_end_unix_ms > now.saturating_add(MAX_CLOCK_SKEW_MS)
        || report.window_start_unix_ms < now.saturating_sub(MAX_REPORT_AGE_MS)
    {
        return Err("HYPERVISOR_NETWORK_REPORT_TIME_INVALID");
    }
    let expected_sequence = u64::try_from(report.window_end_unix_ms.div_euclid(HOURLY_WINDOW_MS))
        .map_err(|_| "HYPERVISOR_NETWORK_REPORT_SEQUENCE_INVALID")?;
    let expected_id = uuid::Uuid::new_v5(
        &REPORT_NAMESPACE,
        format!(
            "{zone_id}:{resource_id}:{}:{}:{expected_sequence}",
            report.window_start_unix_ms, report.window_end_unix_ms
        )
        .as_bytes(),
    );
    if report.sequence != expected_sequence || report_id != expected_id {
        return Err("HYPERVISOR_NETWORK_REPORT_SEQUENCE_INVALID");
    }
    let mut canonical = report.clone();
    canonical.report_sha256.clear();
    if report.report_sha256.as_slice() != Sha256::digest(canonical.encode_to_vec()).as_slice() {
        return Err("HYPERVISOR_NETWORK_REPORT_CHECKSUM_INVALID");
    }
    Ok(report)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn canonical_report() -> HypervisorNetworkUsageReportV1 {
        let zone = uuid::Uuid::new_v4();
        let resource = uuid::Uuid::new_v4();
        let end = chrono::Utc::now()
            .timestamp_millis()
            .div_euclid(HOURLY_WINDOW_MS)
            * HOURLY_WINDOW_MS;
        let start = end - HOURLY_WINDOW_MS;
        let sequence = u64::try_from(end / HOURLY_WINDOW_MS).unwrap();
        let report_id = uuid::Uuid::new_v5(
            &REPORT_NAMESPACE,
            format!("{zone}:{resource}:{start}:{end}:{sequence}").as_bytes(),
        );
        let mut report = HypervisorNetworkUsageReportV1 {
            schema_version: 1,
            report_id: report_id.to_string(),
            zone_id: zone.to_string(),
            resource_id: resource.to_string(),
            window_start_unix_ms: start,
            window_end_unix_ms: end,
            sequence,
            network_in_bytes: 1,
            network_out_bytes: 2,
            report_sha256: Vec::new(),
        };
        report.report_sha256 = Sha256::digest(report.encode_to_vec()).to_vec();
        report
    }

    #[test]
    fn accepts_canonical_closed_report() {
        let report = canonical_report();
        assert_eq!(decode_report(&report.encode_to_vec()).unwrap(), report);
    }

    #[test]
    fn rejects_reused_identity_with_bad_checksum() {
        let mut report = canonical_report();
        report.report_sha256 = vec![0; 32];
        assert_eq!(
            decode_report(&report.encode_to_vec()),
            Err("HYPERVISOR_NETWORK_REPORT_CHECKSUM_INVALID")
        );
    }
}
