use std::time::Duration;

use chrono::Utc;
use prost::Message;
use redis::Value;
use sha2::{Digest, Sha256};
use tokio::sync::watch;
use uuid::Uuid;

use super::accepted_usage_proto::MailAcceptedUsageV1;

pub(super) const STREAM: &str = "aurora:mail:accepted:usage";
pub(super) const GROUP: &str = "cost-engine-mail-accepted-usage-v1";
const DLQ: &str = "aurora:mail:accepted:usage:dlq";
const MAX_EVIDENCE_BYTES: usize = 16 * 1024;
const MAX_CLOCK_SKEW_MS: i64 = 5 * 60 * 1_000;
const MAX_EVIDENCE_AGE_MS: i64 = 30 * 86_400_000;

pub(super) struct StreamEntry {
    pub(super) id: String,
    pub(super) evidence_id: Option<String>,
    pub(super) zone_id: Option<String>,
    pub(super) resource_id: Option<String>,
    pub(super) evidence_sha256: Option<Vec<u8>>,
    pub(super) payload: Option<Vec<u8>>,
}

pub(super) fn decode_evidence(payload: &[u8]) -> Result<MailAcceptedUsageV1, &'static str> {
    if payload.is_empty() || payload.len() > MAX_EVIDENCE_BYTES {
        return Err("MAIL_ACCEPTED_USAGE_SIZE_INVALID");
    }
    let evidence =
        MailAcceptedUsageV1::decode(payload).map_err(|_| "MAIL_ACCEPTED_USAGE_PROTO_INVALID")?;
    let evidence_id =
        Uuid::parse_str(&evidence.evidence_id).map_err(|_| "MAIL_ACCEPTED_USAGE_ID_INVALID")?;
    let zone_id =
        Uuid::parse_str(&evidence.zone_id).map_err(|_| "MAIL_ACCEPTED_USAGE_ZONE_INVALID")?;
    let resource_id = Uuid::parse_str(&evidence.resource_id)
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
    let now = Utc::now().timestamp_millis();
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

pub(super) async fn acknowledge(
    redis_conn: &mut redis::aio::MultiplexedConnection,
    entry_id: &str,
) -> Result<(), String> {
    redis::Script::new("local acked=redis.call('XACK',KEYS[1],ARGV[1],ARGV[2]); if acked==1 then return redis.call('XDEL',KEYS[1],ARGV[2]) end; return 0")
        .key(STREAM).arg(GROUP).arg(entry_id).invoke_async::<_, i32>(redis_conn).await
        .map(|_| ()).map_err(|error| format!("ack Mail accepted usage entry {entry_id}: {error}"))
}

pub(super) async fn quarantine(
    redis_conn: &mut redis::aio::MultiplexedConnection,
    entry_id: &str,
    reason: &str,
) -> Result<(), String> {
    let reason = reason.chars().take(128).collect::<String>();
    redis::Script::new("redis.call('XADD',KEYS[2],'MAXLEN','~',10000,'*','source_entry_id',ARGV[2],'reason',ARGV[3]); local acked=redis.call('XACK',KEYS[1],ARGV[1],ARGV[2]); if acked==1 then redis.call('XDEL',KEYS[1],ARGV[2]) end; return 1")
        .key(STREAM).key(DLQ).arg(GROUP).arg(entry_id).arg(reason)
        .invoke_async::<_, i32>(redis_conn).await.map(|_| ())
        .map_err(|error| format!("quarantine Mail accepted usage entry {entry_id}: {error}"))
}

pub(super) async fn next_entries(
    redis_conn: &mut redis::aio::MultiplexedConnection,
    consumer: &str,
    claim_cursor: &mut String,
    shutdown_rx: &mut watch::Receiver<bool>,
) -> Result<Vec<StreamEntry>, String> {
    let claimed: Value = redis::cmd("XAUTOCLAIM")
        .arg(STREAM)
        .arg(GROUP)
        .arg(consumer)
        .arg(5_000)
        .arg(&*claim_cursor)
        .arg("COUNT")
        .arg(50)
        .query_async(redis_conn)
        .await
        .map_err(|error| format!("XAUTOCLAIM: {error}"))?;
    if let Value::Bulk(parts) = &claimed {
        if let Some(Value::Data(next)) = parts.first() {
            *claim_cursor = String::from_utf8_lossy(next).into_owned();
        }
        if let Some(Value::Bulk(entries)) = parts.get(1) {
            let parsed = parse_entries(entries);
            if !parsed.is_empty() {
                return Ok(parsed);
            }
        }
    }
    if *shutdown_rx.borrow() {
        return Ok(Vec::new());
    }
    let reply: Value = redis::cmd("XREADGROUP")
        .arg("GROUP")
        .arg(GROUP)
        .arg(consumer)
        .arg("BLOCK")
        .arg(2_000)
        .arg("COUNT")
        .arg(50)
        .arg("STREAMS")
        .arg(STREAM)
        .arg(">")
        .query_async(redis_conn)
        .await
        .map_err(|error| format!("XREADGROUP: {error}"))?;
    let Value::Bulk(streams) = reply else {
        return Ok(Vec::new());
    };
    let Some(Value::Bulk(stream_data)) = streams.first() else {
        return Ok(Vec::new());
    };
    let Some(Value::Bulk(entries)) = stream_data.get(1) else {
        return Ok(Vec::new());
    };
    Ok(parse_entries(entries))
}

fn parse_entries(entries: &[Value]) -> Vec<StreamEntry> {
    entries
        .iter()
        .filter_map(|value| {
            let Value::Bulk(parts) = value else {
                return None;
            };
            let Value::Data(id) = parts.first()? else {
                return None;
            };
            let Value::Bulk(fields) = parts.get(1)? else {
                return None;
            };
            let mut entry = StreamEntry {
                id: String::from_utf8_lossy(id).into_owned(),
                evidence_id: None,
                zone_id: None,
                resource_id: None,
                evidence_sha256: None,
                payload: None,
            };
            for field in fields.chunks(2) {
                if let [Value::Data(name), Value::Data(value)] = field {
                    match name.as_slice() {
                        b"evidence_id" => {
                            entry.evidence_id = Some(String::from_utf8_lossy(value).into_owned())
                        }
                        b"zone_id" => {
                            entry.zone_id = Some(String::from_utf8_lossy(value).into_owned())
                        }
                        b"resource_id" => {
                            entry.resource_id = Some(String::from_utf8_lossy(value).into_owned())
                        }
                        b"evidence_sha256" => entry.evidence_sha256 = Some(value.clone()),
                        b"payload" => entry.payload = Some(value.clone()),
                        _ => {}
                    }
                }
            }
            Some(entry)
        })
        .collect()
}

pub(super) async fn wait_or_shutdown(duration: Duration, shutdown_rx: &mut watch::Receiver<bool>) {
    tokio::select! { _ = tokio::time::sleep(duration) => {}, _ = shutdown_rx.changed() => {} }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rejects_non_unit_recipient_quantity() {
        let evidence = MailAcceptedUsageV1 {
            schema_version: 1,
            evidence_id: Uuid::new_v4().to_string(),
            zone_id: Uuid::new_v4().to_string(),
            resource_id: Uuid::new_v4().to_string(),
            accepted_at_unix_ms: Utc::now().timestamp_millis(),
            recipient_quantity: 2,
            evidence_sha256: vec![0; 32],
        };
        assert_eq!(
            decode_evidence(&evidence.encode_to_vec()),
            Err("MAIL_ACCEPTED_USAGE_CONTRACT_INVALID")
        );
    }
}
