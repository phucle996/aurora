use crate::infra::kafka::transport_proto::DeadLetterRecordV1;
use crate::observability::logger::{LogFields, Logger};
use pgwire_replication::Lsn;
use sha2::{Digest, Sha256};

use super::worker::ChangefeedWorker;

impl ChangefeedWorker {
    pub(super) async fn quarantine_change(
        &self,
        wal_end: Lsn,
        tag: u8,
        data: &[u8],
        error: &str,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let payload_hash = format!("{:x}", Sha256::digest(data));
        let identity = format!("{}:{tag}:{payload_hash}", wal_end.0);
        let event_id = uuid::Uuid::new_v5(&uuid::Uuid::NAMESPACE_OID, identity.as_bytes());
        let error_code = changefeed_error_code(error);
        let record = DeadLetterRecordV1 {
            event_id: event_id.as_bytes().to_vec(),
            source_topic: "postgres.logical_changefeed".to_string(),
            source_partition: 0,
            source_offset: i64::try_from(wal_end.0).unwrap_or(i64::MAX),
            error_code: error_code.to_string(),
            error_message: format!(
                "lsn={} tag={} payload_len={} sha256={} error={}",
                wal_end,
                char::from(tag),
                data.len(),
                payload_hash,
                bounded_utf8(error, 256)
            ),
            // WAL row payloads can contain encrypted envelopes or credentials.
            // Quarantine keeps only a fingerprint, never a second raw copy.
            original_payload: Vec::new(),
            failed_at_unix_ms: chrono::Utc::now().timestamp_millis(),
            schema_version: 1,
        };
        self.kafka
            .publish_message(
                &self.kafka.dead_letter_topic(),
                event_id.as_bytes(),
                &record,
            )
            .await
            .map_err(std::io::Error::other)?;
        crate::observability::metrics::MetricsManager::record_wal_rejected();
        crate::observability::metrics::MetricsManager::record_dlq_published();
        let event_id_text = event_id.to_string();
        Logger::sys_warn_with_fields(
            "changefeed.quarantine",
            error_code,
            "Permanent WAL error was durably quarantined before LSN advance",
            "",
            LogFields {
                event_id: Some(&event_id_text),
                outcome: Some("quarantined"),
                ..LogFields::default()
            },
        );
        Ok(())
    }
}

pub(super) fn canonical_zone_route(zone_id: &str) -> Result<String, String> {
    let parsed_zone_id = uuid::Uuid::parse_str(zone_id)
        .map_err(|error| format!("runtime command requires a valid zone UUID: {error}"))?;
    if parsed_zone_id.is_nil() {
        return Err("runtime command requires a non-nil zone UUID".to_string());
    }
    Ok(parsed_zone_id.to_string())
}

/// Giải mã chuỗi hex biểu diễn cột BYTEA trong replication message của Postgres
pub(super) fn decode_pg_bytea(val: &str) -> Result<Vec<u8>, Box<dyn std::error::Error>> {
    if !val.starts_with("\\x") {
        return Ok(val.as_bytes().to_vec());
    }
    let hex_part = &val[2..];
    if !hex_part.len().is_multiple_of(2) {
        return Err("Invalid hex length for pg bytea".into());
    }
    let mut bytes = Vec::with_capacity(hex_part.len() / 2);
    for i in (0..hex_part.len()).step_by(2) {
        let chunk = &hex_part[i..i + 2];
        let byte = u8::from_str_radix(chunk, 16)?;
        bytes.push(byte);
    }
    Ok(bytes)
}

fn bounded_utf8(value: &str, max_bytes: usize) -> String {
    if value.len() <= max_bytes {
        return value.to_string();
    }
    let mut end = max_bytes;
    while !value.is_char_boundary(end) {
        end -= 1;
    }
    value[..end].to_string()
}

pub(super) fn changefeed_error_code(error: &str) -> &'static str {
    if error.contains("target Zone")
        || error.contains("target Zone UUID")
        || error.contains("requires a valid zone UUID")
    {
        "COMMAND_ZONE_MISMATCH"
    } else if error.contains("protected payload metadata") || error.contains("payload_key_id") {
        "COMMAND_HASH_MISMATCH"
    } else {
        "COMMAND_CONTRACT_INVALID"
    }
}
