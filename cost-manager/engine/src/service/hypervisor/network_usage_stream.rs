use std::time::Duration;

use chrono::Utc;
use prost::Message;
use redis::Value;
use sha2::{Digest, Sha256};
use tokio::sync::watch;
use uuid::Uuid;

use super::network_usage_proto::HypervisorNetworkUsageReportV1;

pub(super) const STREAM: &str = "aurora:hypervisor:network:usage:reports";
pub(super) const GROUP: &str = "cost-engine-hypervisor-network-usage-v1";
const DLQ: &str = "aurora:hypervisor:network:usage:reports:dlq";
const HOUR_MS: i64 = 3_600_000;
const MAX_REPORT_BYTES: usize = 64 * 1024;
const MAX_CLOCK_SKEW_MS: i64 = 5 * 60 * 1_000;
const MAX_REPORT_AGE_MS: i64 = 30 * 86_400_000;
const REPORT_NAMESPACE: Uuid = Uuid::from_u128(0x98a4_181b_0674_5ca5_a3a1_d2ba_fbd5_1921);

pub(super) struct StreamEntry {
    pub(super) id: String,
    pub(super) report_id: Option<String>,
    pub(super) zone_id: Option<String>,
    pub(super) resource_id: Option<String>,
    pub(super) report_sha256: Option<Vec<u8>>,
    pub(super) payload: Option<Vec<u8>>,
}

pub(super) fn decode_report(
    payload: &[u8],
) -> Result<HypervisorNetworkUsageReportV1, &'static str> {
    if payload.is_empty() || payload.len() > MAX_REPORT_BYTES {
        return Err("HYPERVISOR_NETWORK_REPORT_SIZE_INVALID");
    }
    let report = HypervisorNetworkUsageReportV1::decode(payload)
        .map_err(|_| "HYPERVISOR_NETWORK_REPORT_PROTO_INVALID")?;
    let report_id =
        Uuid::parse_str(&report.report_id).map_err(|_| "HYPERVISOR_NETWORK_REPORT_ID_INVALID")?;
    let zone_id =
        Uuid::parse_str(&report.zone_id).map_err(|_| "HYPERVISOR_NETWORK_REPORT_ZONE_INVALID")?;
    let resource_id = Uuid::parse_str(&report.resource_id)
        .map_err(|_| "HYPERVISOR_NETWORK_REPORT_RESOURCE_INVALID")?;
    if report.schema_version != 1
        || report_id.is_nil()
        || zone_id.is_nil()
        || resource_id.is_nil()
        || report.window_end_unix_ms - report.window_start_unix_ms != HOUR_MS
        || report.window_start_unix_ms.rem_euclid(HOUR_MS) != 0
        || report.network_in_bytes == 0 && report.network_out_bytes == 0
        || report.report_sha256.len() != 32
        || i64::try_from(report.network_in_bytes).is_err()
        || i64::try_from(report.network_out_bytes).is_err()
    {
        return Err("HYPERVISOR_NETWORK_REPORT_CONTRACT_INVALID");
    }
    let now = Utc::now().timestamp_millis();
    if report.window_end_unix_ms > now.saturating_add(MAX_CLOCK_SKEW_MS)
        || report.window_start_unix_ms < now.saturating_sub(MAX_REPORT_AGE_MS)
    {
        return Err("HYPERVISOR_NETWORK_REPORT_TIME_INVALID");
    }
    let sequence = u64::try_from(report.window_end_unix_ms.div_euclid(HOUR_MS))
        .map_err(|_| "HYPERVISOR_NETWORK_REPORT_SEQUENCE_INVALID")?;
    let expected_id = Uuid::new_v5(
        &REPORT_NAMESPACE,
        format!(
            "{zone_id}:{resource_id}:{}:{}:{sequence}",
            report.window_start_unix_ms, report.window_end_unix_ms
        )
        .as_bytes(),
    );
    if report.sequence != sequence || report_id != expected_id {
        return Err("HYPERVISOR_NETWORK_REPORT_SEQUENCE_INVALID");
    }
    let mut canonical = report.clone();
    canonical.report_sha256.clear();
    if report.report_sha256.as_slice() != Sha256::digest(canonical.encode_to_vec()).as_slice() {
        return Err("HYPERVISOR_NETWORK_REPORT_CHECKSUM_INVALID");
    }
    Ok(report)
}

pub(super) async fn acknowledge(
    redis_conn: &mut redis::aio::MultiplexedConnection,
    entry_id: &str,
) -> Result<(), String> {
    redis::Script::new("local acked=redis.call('XACK',KEYS[1],ARGV[1],ARGV[2]); if acked==1 then return redis.call('XDEL',KEYS[1],ARGV[2]) end; return 0")
        .key(STREAM).arg(GROUP).arg(entry_id).invoke_async::<_, i32>(redis_conn).await
        .map(|_| ()).map_err(|error| format!("ack Hypervisor network entry {entry_id}: {error}"))
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
        .map_err(|error| format!("quarantine Hypervisor network entry {entry_id}: {error}"))
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
                report_id: None,
                zone_id: None,
                resource_id: None,
                report_sha256: None,
                payload: None,
            };
            for field in fields.chunks(2) {
                if let [Value::Data(name), Value::Data(value)] = field {
                    match name.as_slice() {
                        b"report_id" => {
                            entry.report_id = Some(String::from_utf8_lossy(value).into_owned())
                        }
                        b"zone_id" => {
                            entry.zone_id = Some(String::from_utf8_lossy(value).into_owned())
                        }
                        b"resource_id" => {
                            entry.resource_id = Some(String::from_utf8_lossy(value).into_owned())
                        }
                        b"report_sha256" => entry.report_sha256 = Some(value.clone()),
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
    fn rejects_non_hourly_report() {
        let report = HypervisorNetworkUsageReportV1 {
            schema_version: 1,
            report_id: Uuid::new_v4().to_string(),
            zone_id: Uuid::new_v4().to_string(),
            resource_id: Uuid::new_v4().to_string(),
            window_start_unix_ms: 0,
            window_end_unix_ms: 1,
            sequence: 1,
            network_in_bytes: 1,
            network_out_bytes: 0,
            report_sha256: vec![0; 32],
        };
        assert_eq!(
            decode_report(&report.encode_to_vec()),
            Err("HYPERVISOR_NETWORK_REPORT_CONTRACT_INVALID")
        );
    }
}
