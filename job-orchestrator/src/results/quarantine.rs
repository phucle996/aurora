use crate::infra::kafka::transport_proto::DeadLetterRecordV1;
use crate::infra::kafka::KafkaTransport;
use sha2::{Digest, Sha256};

pub async fn publish(
    kafka: &KafkaTransport,
    source_topic: &str,
    source_partition: i32,
    source_offset: i64,
    error_code: &str,
    payload: &[u8],
) -> Result<uuid::Uuid, String> {
    let event_id = event_id(source_topic, source_partition, source_offset, error_code);
    let payload_hash = format!("{:x}", Sha256::digest(payload));
    let record = DeadLetterRecordV1 {
        event_id: event_id.as_bytes().to_vec(),
        source_topic: source_topic.to_string(),
        source_partition,
        source_offset,
        error_code: error_code.to_string(),
        // [COMMENT]: Raw result bytes can contain provider diagnostics. Keep only
        // a bounded fingerprint so quarantine cannot become a secret copy.
        error_message: format!("payload_len={} sha256={payload_hash}", payload.len()),
        original_payload: Vec::new(),
        failed_at_unix_ms: chrono::Utc::now().timestamp_millis(),
        schema_version: 1,
    };
    kafka
        .publish_message(&kafka.dead_letter_topic(), event_id.as_bytes(), &record)
        .await?;
    Ok(event_id)
}

fn event_id(
    source_topic: &str,
    source_partition: i32,
    source_offset: i64,
    error_code: &str,
) -> uuid::Uuid {
    let identity = format!("{source_topic}:{source_partition}:{source_offset}:{error_code}");
    uuid::Uuid::new_v5(&uuid::Uuid::NAMESPACE_URL, identity.as_bytes())
}

#[cfg(test)]
mod tests {
    use super::event_id;

    #[test]
    fn quarantine_identity_is_stable_per_source_record() {
        assert_eq!(
            event_id("results", 2, 41, "INVALID"),
            event_id("results", 2, 41, "INVALID")
        );
        assert_ne!(
            event_id("results", 2, 41, "INVALID"),
            event_id("results", 2, 42, "INVALID")
        );
    }
}
