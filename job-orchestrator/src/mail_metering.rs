use std::sync::Arc;
use std::time::Duration;

use prost::Message;
use sha2::{Digest, Sha256};

use crate::config::Config;
use crate::contracts::mail_accepted_usage::MailAcceptedUsageV1;
use crate::infra::kafka::{transport_proto::DeadLetterRecordV1, KafkaTransport};

const STREAM: &str = "aurora:mail:accepted:usage";
const MAX_EVIDENCE_BYTES: usize = 16 * 1024;
const MAX_CLOCK_SKEW_MS: i64 = 5 * 60 * 1_000;
const MAX_EVIDENCE_AGE_MS: i64 = 30 * 86_400_000;
const STREAM_CAPACITY: i64 = 1_000_000;

pub async fn run_accepted_usage_relay(
    config: &Config,
    kafka: Arc<KafkaTransport>,
    redis_client: &redis::Client,
) -> Result<(), Box<dyn std::error::Error>> {
    let mut redis_conn =
        crate::infra::redis::multiplexed(redis_client, &config.shared_redis).await?;
    let topic = kafka.mail_accepted_usage_topic();
    let consumer = kafka
        .consumer("aurora-job-orchestrator-mail-accepted-usage-v1", &topic)
        .await
        .map_err(std::io::Error::other)?;
    loop {
        for record in consumer.poll(Duration::from_secs(1)).await? {
            let payload = record.value.unwrap_or_default();
            let evidence = match decode_evidence(&payload) {
                Ok(evidence) => evidence,
                Err(error_code) => {
                    let dlq = DeadLetterRecordV1 {
                        event_id: uuid::Uuid::new_v4().as_bytes().to_vec(),
                        source_topic: record.topic.clone(),
                        source_partition: record.partition,
                        source_offset: record.offset,
                        error_code: error_code.to_string(),
                        error_message: "MailAcceptedUsageV1 failed strict validation".to_string(),
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
                "if redis.call('XLEN',KEYS[1]) >= tonumber(ARGV[1]) then return redis.error_reply('MAIL_ACCEPTED_USAGE_STREAM_CAPACITY_REACHED') end; return redis.call('XADD',KEYS[1],'*','evidence_id',ARGV[2],'zone_id',ARGV[3],'resource_id',ARGV[4],'evidence_sha256',ARGV[5],'payload',ARGV[6])",
            )
            .key(STREAM)
            .arg(STREAM_CAPACITY)
            .arg(&evidence.evidence_id)
            .arg(&evidence.zone_id)
            .arg(&evidence.resource_id)
            .arg(evidence.evidence_sha256.as_slice())
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
                    "Mail accepted usage Redis durability fence not met: local={local_aof}, replicas={replica_aof}, required={}",
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

fn decode_evidence(payload: &[u8]) -> Result<MailAcceptedUsageV1, &'static str> {
    if payload.is_empty() || payload.len() > MAX_EVIDENCE_BYTES {
        return Err("MAIL_ACCEPTED_USAGE_SIZE_INVALID");
    }
    let evidence =
        MailAcceptedUsageV1::decode(payload).map_err(|_| "MAIL_ACCEPTED_USAGE_PROTO_INVALID")?;
    let evidence_id = uuid::Uuid::parse_str(&evidence.evidence_id)
        .map_err(|_| "MAIL_ACCEPTED_USAGE_ID_INVALID")?;
    let zone_id =
        uuid::Uuid::parse_str(&evidence.zone_id).map_err(|_| "MAIL_ACCEPTED_USAGE_ZONE_INVALID")?;
    let resource_id = uuid::Uuid::parse_str(&evidence.resource_id)
        .map_err(|_| "MAIL_ACCEPTED_USAGE_RESOURCE_INVALID")?;
    if evidence.schema_version != 1
        || evidence_id.is_nil()
        || zone_id.is_nil()
        || resource_id.is_nil()
        || evidence.recipient_quantity != 1
        || evidence.evidence_sha256.len() != 32
    {
        return Err("MAIL_ACCEPTED_USAGE_CONTRACT_INVALID");
    }
    let now = chrono::Utc::now().timestamp_millis();
    if evidence.accepted_at_unix_ms <= 0
        || evidence.accepted_at_unix_ms > now.saturating_add(MAX_CLOCK_SKEW_MS)
        || evidence.accepted_at_unix_ms < now.saturating_sub(MAX_EVIDENCE_AGE_MS)
    {
        return Err("MAIL_ACCEPTED_USAGE_TIME_INVALID");
    }
    let mut canonical = evidence.clone();
    canonical.evidence_sha256.clear();
    if evidence.evidence_sha256.as_slice() != Sha256::digest(canonical.encode_to_vec()).as_slice() {
        return Err("MAIL_ACCEPTED_USAGE_CHECKSUM_INVALID");
    }
    Ok(evidence)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn canonical_evidence() -> MailAcceptedUsageV1 {
        let mut evidence = MailAcceptedUsageV1 {
            schema_version: 1,
            evidence_id: uuid::Uuid::new_v4().to_string(),
            zone_id: uuid::Uuid::new_v4().to_string(),
            resource_id: uuid::Uuid::new_v4().to_string(),
            accepted_at_unix_ms: chrono::Utc::now().timestamp_millis(),
            recipient_quantity: 1,
            evidence_sha256: Vec::new(),
        };
        evidence.evidence_sha256 = Sha256::digest(evidence.encode_to_vec()).to_vec();
        evidence
    }

    #[test]
    fn accepts_one_content_free_recipient_evidence() {
        let evidence = canonical_evidence();
        assert_eq!(
            decode_evidence(&evidence.encode_to_vec()).unwrap(),
            evidence
        );
    }

    #[test]
    fn rejects_more_than_one_recipient_per_evidence() {
        let mut evidence = canonical_evidence();
        evidence.recipient_quantity = 2;
        evidence.evidence_sha256.clear();
        evidence.evidence_sha256 = Sha256::digest(evidence.encode_to_vec()).to_vec();
        assert_eq!(
            decode_evidence(&evidence.encode_to_vec()),
            Err("MAIL_ACCEPTED_USAGE_CONTRACT_INVALID")
        );
    }
}
