use std::time::Duration;

use chrono::{DateTime, Utc};
use prost::Message;
use redis::Value;
use sha2::{Digest, Sha256};
use sqlx::{PgPool, Row};
use tokio::sync::watch;
use uuid::Uuid;

use super::ALLOCATION_SHARD_COUNT;
use super::allocation_proto::HypervisorAllocationChangedV1;

const STREAM: &str = "stream:{billing}:hypervisor_allocation";
const GROUP: &str = "cost-engine-hypervisor-allocation-lifecycle-v1";
const DLQ: &str = "stream:{billing}:hypervisor_allocation:dlq";
const MAX_PAYLOAD_BYTES: usize = 64 * 1024;

struct StreamEntry {
    id: String,
    event_id: Option<String>,
    event_type: Option<String>,
    payload: Option<Vec<u8>>,
}

enum ApplyFailure {
    Integrity(String),
    Retry(String),
}

pub async fn run_hypervisor_allocation_lifecycle(
    pg_pool: PgPool,
    mut redis_conn: redis::aio::MultiplexedConnection,
    mut shutdown_rx: watch::Receiver<bool>,
) {
    let group_result: redis::RedisResult<()> = redis::cmd("XGROUP")
        .arg("CREATE")
        .arg(STREAM)
        .arg(GROUP)
        .arg("0-0")
        .arg("MKSTREAM")
        .query_async(&mut redis_conn)
        .await;
    if let Err(error) = group_result
        && !error.to_string().contains("BUSYGROUP")
    {
        eprintln!("Hypervisor allocation lifecycle cannot create Redis group: {error}");
        return;
    }
    let consumer = format!(
        "cost-engine-{}-{}",
        std::env::var("HOSTNAME").unwrap_or_else(|_| "local".to_string()),
        std::process::id()
    );
    let mut claim_cursor = "0-0".to_string();
    loop {
        if *shutdown_rx.borrow() {
            return;
        }
        let entries = match next_entries(
            &mut redis_conn,
            &consumer,
            &mut claim_cursor,
            &mut shutdown_rx,
        )
        .await
        {
            Ok(entries) => entries,
            Err(error) => {
                eprintln!("Hypervisor allocation lifecycle stream read failed: {error}");
                wait_or_shutdown(Duration::from_secs(2), &mut shutdown_rx).await;
                continue;
            }
        };
        for entry in entries {
            let payload = match entry.payload.as_deref() {
                Some(payload) => payload,
                None => {
                    let _ =
                        quarantine(&mut redis_conn, &entry.id, "ALLOCATION_PAYLOAD_MISSING").await;
                    continue;
                }
            };
            let event = match decode_event(payload) {
                Ok(event) => event,
                Err(error) => {
                    let _ = quarantine(&mut redis_conn, &entry.id, error).await;
                    continue;
                }
            };
            let event_id = match uuid_from_bytes(&event.event_id) {
                Ok(id) => id,
                Err(error) => {
                    let _ = quarantine(&mut redis_conn, &entry.id, error).await;
                    continue;
                }
            };
            if entry.event_id.as_deref() != Some(event_id.to_string().as_str())
                || entry.event_type.as_deref() != Some(event.event_type.as_str())
            {
                let _ = quarantine(
                    &mut redis_conn,
                    &entry.id,
                    "ALLOCATION_STREAM_FIELDS_MISMATCH",
                )
                .await;
                continue;
            }
            match apply_event(&pg_pool, &event, payload).await {
                Ok(()) => {
                    if let Err(error) = acknowledge(&mut redis_conn, &entry.id).await {
                        eprintln!("Hypervisor allocation ACK failed: {error}");
                        break;
                    }
                }
                Err(ApplyFailure::Integrity(error)) => {
                    let _ = quarantine(&mut redis_conn, &entry.id, &error).await;
                }
                Err(ApplyFailure::Retry(error)) => {
                    eprintln!("Hypervisor allocation event remains pending: {error}");
                    wait_or_shutdown(Duration::from_secs(1), &mut shutdown_rx).await;
                    break;
                }
            }
        }
    }
}

async fn apply_event(
    pg_pool: &PgPool,
    event: &HypervisorAllocationChangedV1,
    payload: &[u8],
) -> Result<(), ApplyFailure> {
    let event_id = uuid_from_bytes(&event.event_id)
        .map_err(|error| ApplyFailure::Integrity(error.to_string()))?;
    let resource_id = uuid_from_bytes(&event.resource_id)
        .map_err(|error| ApplyFailure::Integrity(error.to_string()))?;
    let zone_id = uuid_from_bytes(&event.zone_id)
        .map_err(|error| ApplyFailure::Integrity(error.to_string()))?;
    let effective_at = DateTime::<Utc>::from_timestamp_millis(event.effective_at_unix_ms)
        .ok_or_else(|| ApplyFailure::Integrity("ALLOCATION_EFFECTIVE_AT_INVALID".to_string()))?;
    let source_version = i64::try_from(event.source_version)
        .map_err(|_| ApplyFailure::Integrity("ALLOCATION_VERSION_OVERFLOW".to_string()))?;
    let cpu_cores = i64::try_from(event.cpu_cores)
        .map_err(|_| ApplyFailure::Integrity("ALLOCATION_CPU_OVERFLOW".to_string()))?;
    let memory_mib = i64::try_from(event.memory_mib)
        .map_err(|_| ApplyFailure::Integrity("ALLOCATION_MEMORY_OVERFLOW".to_string()))?;
    let disk_gib = i64::try_from(event.disk_gib)
        .map_err(|_| ApplyFailure::Integrity("ALLOCATION_DISK_OVERFLOW".to_string()))?;
    let gpu_count = i64::try_from(event.gpu_count)
        .map_err(|_| ApplyFailure::Integrity("ALLOCATION_GPU_OVERFLOW".to_string()))?;
    let gpu_sku = (!event.gpu_sku.is_empty()).then_some(event.gpu_sku.as_str());
    let payload_hash = format!("{:x}", Sha256::digest(payload));

    let mut tx = pg_pool.begin().await.map_err(|error| {
        ApplyFailure::Retry(format!("begin allocation event transaction: {error}"))
    })?;
    let inserted = sqlx::query(
        "INSERT INTO billing.hypervisor_allocation_event_inbox
         (event_id,event_type,payload_hash,resource_id,source_version,status)
         VALUES ($1,$2,$3,$4,$5,'RECEIVED')
         ON CONFLICT (event_id) DO NOTHING RETURNING event_id",
    )
    .bind(event_id)
    .bind(&event.event_type)
    .bind(&payload_hash)
    .bind(resource_id)
    .bind(source_version)
    .fetch_optional(&mut *tx)
    .await
    .map_err(|error| ApplyFailure::Retry(format!("insert allocation event inbox: {error}")))?;
    if inserted.is_none() {
        let stored: (String, String) = sqlx::query_as(
            "SELECT payload_hash,status FROM billing.hypervisor_allocation_event_inbox WHERE event_id=$1",
        )
        .bind(event_id)
        .fetch_one(&mut *tx)
        .await
        .map_err(|error| ApplyFailure::Retry(format!("read duplicate allocation event: {error}")))?;
        if stored.0 != payload_hash {
            return Err(ApplyFailure::Integrity(
                "ALLOCATION_EVENT_ID_PAYLOAD_CONFLICT".to_string(),
            ));
        }
        tx.commit().await.map_err(|error| {
            ApplyFailure::Retry(format!("commit duplicate allocation event: {error}"))
        })?;
        return Ok(());
    }

    sqlx::query("SELECT pg_advisory_xact_lock(hashtextextended($1,0))")
        .bind(resource_id.to_string())
        .execute(&mut *tx)
        .await
        .map_err(|error| ApplyFailure::Retry(format!("lock allocation resource: {error}")))?;
    let head = sqlx::query(
        "SELECT zone_id,last_source_version,state FROM billing.hypervisor_allocation_heads WHERE resource_id=$1 FOR UPDATE",
    )
    .bind(resource_id)
    .fetch_optional(&mut *tx)
    .await
    .map_err(|error| ApplyFailure::Retry(format!("read allocation head: {error}")))?;

    let touches_settled_window: bool = sqlx::query_scalar(
        "SELECT EXISTS (
             SELECT 1
             FROM billing.hypervisor_allocation_windows
             WHERE zone_id=$1
               AND shard_id=mod((hashtextextended($2,0) & 9223372036854775807), $3::bigint)::int
               AND status='SETTLED'
               AND window_end > $4
         )",
    )
    .bind(zone_id)
    .bind(resource_id.to_string())
    .bind(i64::from(ALLOCATION_SHARD_COUNT))
    .bind(effective_at)
    .fetch_one(&mut *tx)
    .await
    .map_err(|error| ApplyFailure::Retry(format!("check settled allocation boundary: {error}")))?;
    if touches_settled_window {
        return Err(ApplyFailure::Integrity(
            "ALLOCATION_EVENT_AFTER_SETTLED_WINDOW".to_string(),
        ));
    }

    match event.event_type.as_str() {
        "ACTIVATE" => {
            if source_version != 1 || head.is_some() {
                return Err(ApplyFailure::Integrity(
                    "ALLOCATION_ACTIVATION_STATE_INVALID".to_string(),
                ));
            }
            sqlx::query(
                "INSERT INTO billing.hypervisor_allocation_intervals
                 (id,resource_id,zone_id,allocation_version,effective_from,cpu_cores,memory_mib,disk_gib,gpu_sku,gpu_count,source_event_id)
                 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$1)",
            )
            .bind(event_id)
            .bind(resource_id)
            .bind(zone_id)
            .bind(source_version)
            .bind(effective_at)
            .bind(cpu_cores)
            .bind(memory_mib)
            .bind(disk_gib)
            .bind(gpu_sku)
            .bind(gpu_count)
            .execute(&mut *tx)
            .await
            .map_err(|error| ApplyFailure::Retry(format!("insert allocation interval: {error}")))?;
            sqlx::query(
                "INSERT INTO billing.hypervisor_allocation_heads
                 (resource_id,zone_id,last_source_version,state) VALUES ($1,$2,$3,'ACTIVE')",
            )
            .bind(resource_id)
            .bind(zone_id)
            .bind(source_version)
            .execute(&mut *tx)
            .await
            .map_err(|error| ApplyFailure::Retry(format!("insert allocation head: {error}")))?;
        }
        "REVISE" => {
            let Some(head) = head else {
                return Err(ApplyFailure::Retry(
                    "ALLOCATION_REVISION_PREDECESSOR_PENDING".to_string(),
                ));
            };
            let head_zone: Uuid = head.get(0);
            let last_version: i64 = head.get(1);
            let state: String = head.get(2);
            if head_zone != zone_id || state != "ACTIVE" || source_version <= last_version {
                return Err(ApplyFailure::Integrity(
                    "ALLOCATION_REVISION_STATE_INVALID".to_string(),
                ));
            }
            if source_version > last_version + 1 {
                return Err(ApplyFailure::Retry(
                    "ALLOCATION_REVISION_PREDECESSOR_PENDING".to_string(),
                ));
            }
            let closed = sqlx::query(
                "UPDATE billing.hypervisor_allocation_intervals SET effective_to=$1
                 WHERE resource_id=$2 AND effective_to IS NULL AND effective_from < $1",
            )
            .bind(effective_at)
            .bind(resource_id)
            .execute(&mut *tx)
            .await
            .map_err(|error| {
                ApplyFailure::Retry(format!("close prior allocation interval: {error}"))
            })?;
            if closed.rows_affected() != 1 {
                return Err(ApplyFailure::Integrity(
                    "ALLOCATION_REVISION_BOUNDARY_INVALID".to_string(),
                ));
            }
            sqlx::query(
                "INSERT INTO billing.hypervisor_allocation_intervals
                 (id,resource_id,zone_id,allocation_version,effective_from,cpu_cores,memory_mib,disk_gib,gpu_sku,gpu_count,source_event_id)
                 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$1)",
            )
            .bind(event_id)
            .bind(resource_id)
            .bind(zone_id)
            .bind(source_version)
            .bind(effective_at)
            .bind(cpu_cores)
            .bind(memory_mib)
            .bind(disk_gib)
            .bind(gpu_sku)
            .bind(gpu_count)
            .execute(&mut *tx)
            .await
            .map_err(|error| ApplyFailure::Retry(format!("insert revised allocation interval: {error}")))?;
            sqlx::query(
                "UPDATE billing.hypervisor_allocation_heads SET last_source_version=$1,updated_at=NOW() WHERE resource_id=$2",
            )
            .bind(source_version)
            .bind(resource_id)
            .execute(&mut *tx)
            .await
            .map_err(|error| ApplyFailure::Retry(format!("advance allocation head: {error}")))?;
        }
        "TERMINATE" => {
            let Some(head) = head else {
                return Err(ApplyFailure::Retry(
                    "ALLOCATION_TERMINATION_PREDECESSOR_PENDING".to_string(),
                ));
            };
            let head_zone: Uuid = head.get(0);
            let last_version: i64 = head.get(1);
            let state: String = head.get(2);
            if head_zone != zone_id || state != "ACTIVE" || source_version <= last_version {
                return Err(ApplyFailure::Integrity(
                    "ALLOCATION_TERMINATION_STATE_INVALID".to_string(),
                ));
            }
            if source_version > last_version + 1 {
                return Err(ApplyFailure::Retry(
                    "ALLOCATION_TERMINATION_PREDECESSOR_PENDING".to_string(),
                ));
            }
            let closed = sqlx::query(
                "UPDATE billing.hypervisor_allocation_intervals SET effective_to=$1
                 WHERE resource_id=$2 AND effective_to IS NULL AND effective_from < $1",
            )
            .bind(effective_at)
            .bind(resource_id)
            .execute(&mut *tx)
            .await
            .map_err(|error| {
                ApplyFailure::Retry(format!("close terminal allocation interval: {error}"))
            })?;
            if closed.rows_affected() != 1 {
                return Err(ApplyFailure::Integrity(
                    "ALLOCATION_TERMINATION_BOUNDARY_INVALID".to_string(),
                ));
            }
            sqlx::query(
                "UPDATE billing.hypervisor_allocation_heads SET last_source_version=$1,state='TERMINATED',updated_at=NOW() WHERE resource_id=$2",
            )
            .bind(source_version)
            .bind(resource_id)
            .execute(&mut *tx)
            .await
            .map_err(|error| ApplyFailure::Retry(format!("terminate allocation head: {error}")))?;
        }
        _ => {
            return Err(ApplyFailure::Integrity(
                "ALLOCATION_EVENT_TYPE_INVALID".to_string(),
            ));
        }
    }
    sqlx::query(
        "UPDATE billing.hypervisor_allocation_event_inbox SET status='APPLIED',processed_at=NOW() WHERE event_id=$1",
    )
    .bind(event_id)
    .execute(&mut *tx)
    .await
    .map_err(|error| ApplyFailure::Retry(format!("complete allocation inbox: {error}")))?;
    tx.commit()
        .await
        .map_err(|error| ApplyFailure::Retry(format!("commit allocation event: {error}")))?;
    Ok(())
}

fn decode_event(payload: &[u8]) -> Result<HypervisorAllocationChangedV1, &'static str> {
    if payload.is_empty() || payload.len() > MAX_PAYLOAD_BYTES {
        return Err("ALLOCATION_PAYLOAD_SIZE_INVALID");
    }
    let event =
        HypervisorAllocationChangedV1::decode(payload).map_err(|_| "ALLOCATION_PROTO_INVALID")?;
    let event_id = uuid_from_bytes(&event.event_id)?;
    let resource_id = uuid_from_bytes(&event.resource_id)?;
    let zone_id = uuid_from_bytes(&event.zone_id)?;
    let source_job_id = uuid_from_bytes(&event.source_job_id)?;
    let effective_at = DateTime::<Utc>::from_timestamp_millis(event.effective_at_unix_ms)
        .ok_or("ALLOCATION_EFFECTIVE_AT_INVALID")?;
    if event.schema_version != 1
        || event_id.is_nil()
        || resource_id.is_nil()
        || zone_id.is_nil()
        || source_job_id.is_nil()
        || event.source_version == 0
        || effective_at > Utc::now() + chrono::Duration::minutes(5)
    {
        return Err("ALLOCATION_CONTRACT_INVALID");
    }
    match event.event_type.as_str() {
        "ACTIVATE" | "REVISE" => {
            if event.cpu_cores == 0
                || event.cpu_cores > 1024
                || event.memory_mib == 0
                || event.memory_mib > 4_194_304
                || event.disk_gib == 0
                || event.disk_gib > 1_048_576
                || (event.gpu_count == 0 && !event.gpu_sku.is_empty())
                || (event.gpu_count > 0
                    && (event.gpu_count > 64
                        || event.gpu_sku.trim().is_empty()
                        || event.gpu_sku.len() > 64))
            {
                return Err("ALLOCATION_LIMITS_INVALID");
            }
        }
        "TERMINATE" => {
            if event.cpu_cores != 0
                || event.memory_mib != 0
                || event.disk_gib != 0
                || event.gpu_count != 0
                || !event.gpu_sku.is_empty()
            {
                return Err("ALLOCATION_TERMINATION_PAYLOAD_INVALID");
            }
        }
        _ => return Err("ALLOCATION_EVENT_TYPE_INVALID"),
    }
    Ok(event)
}

fn uuid_from_bytes(bytes: &[u8]) -> Result<Uuid, &'static str> {
    Uuid::from_slice(bytes).map_err(|_| "ALLOCATION_UUID_INVALID")
}

async fn acknowledge(
    redis_conn: &mut redis::aio::MultiplexedConnection,
    entry_id: &str,
) -> Result<(), String> {
    redis::Script::new(
        "local acked=redis.call('XACK',KEYS[1],ARGV[1],ARGV[2]); if acked==1 then return redis.call('XDEL',KEYS[1],ARGV[2]) end; return 0",
    )
    .key(STREAM)
    .arg(GROUP)
    .arg(entry_id)
    .invoke_async::<_, i32>(redis_conn)
    .await
    .map(|_| ())
    .map_err(|error| format!("ack allocation entry {entry_id}: {error}"))
}

async fn quarantine(
    redis_conn: &mut redis::aio::MultiplexedConnection,
    entry_id: &str,
    reason: &str,
) -> Result<(), String> {
    let reason = reason.chars().take(128).collect::<String>();
    redis::Script::new(
        "redis.call('XADD',KEYS[2],'MAXLEN','~',10000,'*','source_entry_id',ARGV[2],'reason',ARGV[3]); local acked=redis.call('XACK',KEYS[1],ARGV[1],ARGV[2]); if acked==1 then redis.call('XDEL',KEYS[1],ARGV[2]) end; return 1",
    )
    .key(STREAM)
    .key(DLQ)
    .arg(GROUP)
    .arg(entry_id)
    .arg(reason)
    .invoke_async::<_, i32>(redis_conn)
    .await
    .map(|_| ())
    .map_err(|error| format!("quarantine allocation entry {entry_id}: {error}"))
}

async fn next_entries(
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
            let entries = parse_entries(entries);
            if !entries.is_empty() {
                return Ok(entries);
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
                event_id: None,
                event_type: None,
                payload: None,
            };
            for field in fields.chunks(2) {
                if let [Value::Data(name), Value::Data(value)] = field {
                    match name.as_slice() {
                        b"event_id" => {
                            entry.event_id = Some(String::from_utf8_lossy(value).into_owned())
                        }
                        b"event_type" => {
                            entry.event_type = Some(String::from_utf8_lossy(value).into_owned())
                        }
                        b"payload" => entry.payload = Some(value.clone()),
                        _ => {}
                    }
                }
            }
            Some(entry)
        })
        .collect()
}

async fn wait_or_shutdown(duration: Duration, shutdown_rx: &mut watch::Receiver<bool>) {
    tokio::select! {
        _ = tokio::time::sleep(duration) => {}
        _ = shutdown_rx.changed() => {}
    }
}

#[cfg(test)]
#[path = "../../../tests/unit/hypervisor_allocation_lifecycle.rs"]
mod tests;
