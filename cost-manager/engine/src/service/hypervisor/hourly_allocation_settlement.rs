use std::collections::HashMap;
use std::sync::Arc;
use std::time::Duration;

use chrono::{DateTime, Timelike, Utc};
use sha2::{Digest, Sha256};
use sqlx::{PgPool, Postgres};
use tokio::sync::watch;
use uuid::Uuid;

use crate::engine::{
    BillingRunCommand, PricingRuntime, RateAdjustmentSnapshot, UsageChargeCommand,
    UsageChargeOutcome, settle_usage_charge,
};

use super::ALLOCATION_SHARD_COUNT;
use super::zone_adjustment_checksum;

const LATE_EVENT_GRACE_MINUTES: i64 = 5;
const LINE_NAMESPACE: Uuid = Uuid::from_u128(0x9cf751bb_eabd_50af_8bb2_49f9422cefbf);

const VCPU_KIND: &str = "hypervisor.vcpu.allocated_second";
const MEMORY_KIND: &str = "hypervisor.memory_mib.allocated_second";
const DISK_KIND: &str = "hypervisor.disk_gib.allocated_second";
const GPU_KIND: &str = "hypervisor.gpu.allocated_second";

struct WindowClaim {
    id: Uuid,
    zone_id: Uuid,
    shard_id: i32,
    start: DateTime<Utc>,
    end: DateTime<Utc>,
    fencing_token: i64,
}

struct AllocationInterval {
    resource_id: Uuid,
    allocation_version: i64,
    effective_from: DateTime<Utc>,
    effective_to: Option<DateTime<Utc>>,
    cpu_cores: i64,
    memory_mib: i64,
    disk_gib: i64,
    gpu_count: i64,
}

struct ComponentLine {
    component: &'static str,
    charge_kind: &'static str,
    usage_unit: &'static str,
    limit: i64,
}

struct AllocationUnratedLine<'a> {
    line_id: Uuid,
    window: &'a WindowClaim,
    resource_id: Uuid,
    quantity: i64,
    charge_kind: &'a str,
    usage_unit: &'a str,
    pricing_version_id: Uuid,
    evidence_hash: &'a str,
    reason: &'a str,
}

pub async fn run_hypervisor_hourly_allocation_settlement(
    pg_pool: PgPool,
    pricing_runtime: Arc<PricingRuntime>,
    mut shutdown_rx: watch::Receiver<bool>,
) {
    loop {
        if *shutdown_rx.borrow() {
            return;
        }
        if let Err(error) = ensure_next_windows(&pg_pool).await {
            eprintln!("Hypervisor allocation window planning failed: {error}");
            wait_or_shutdown(Duration::from_secs(2), &mut shutdown_rx).await;
            continue;
        }
        let claim = match claim_window(&pg_pool).await {
            Ok(claim) => claim,
            Err(error) => {
                eprintln!("Hypervisor allocation window claim failed: {error}");
                wait_or_shutdown(Duration::from_secs(2), &mut shutdown_rx).await;
                continue;
            }
        };
        let Some(claim) = claim else {
            wait_or_shutdown(Duration::from_secs(2), &mut shutdown_rx).await;
            continue;
        };
        if let Err(error) = settle_window(&pg_pool, &pricing_runtime, &claim).await {
            let bounded = error.chars().take(512).collect::<String>();
            let _ = sqlx::query(
                "UPDATE billing.hypervisor_allocation_windows
                 SET status='UNRATED',last_error=$1,updated_at=NOW()
                 WHERE id=$2 AND status='PROCESSING'",
            )
            .bind(&bounded)
            .bind(claim.id)
            .execute(&pg_pool)
            .await;
            eprintln!(
                "Hypervisor allocation window {} remains unrated: {error}",
                claim.id
            );
        }
    }
}

async fn ensure_next_windows(pg_pool: &PgPool) -> Result<(), String> {
    let eligible_end = (Utc::now() - chrono::Duration::minutes(LATE_EVENT_GRACE_MINUTES))
        .with_minute(0)
        .and_then(|value| value.with_second(0))
        .and_then(|value| value.with_nanosecond(0))
        .ok_or_else(|| "cannot derive UTC Hypervisor window boundary".to_string())?;
    sqlx::query(
        "WITH interval_sources AS (
             SELECT zone_id,
                    mod((hashtextextended(resource_id::text,0) & 9223372036854775807), $1::bigint)::int AS shard_id,
                    date_trunc('hour',MIN(effective_from)) AS first_start
             FROM billing.hypervisor_allocation_intervals
             GROUP BY zone_id, mod((hashtextextended(resource_id::text,0) & 9223372036854775807), $1::bigint)::int
         ), candidates AS (
             SELECT source.zone_id,source.shard_id,
                    COALESCE(MAX(allocation_window.window_end),source.first_start) AS window_start
             FROM interval_sources source
             LEFT JOIN billing.hypervisor_allocation_windows allocation_window
               ON allocation_window.zone_id=source.zone_id AND allocation_window.shard_id=source.shard_id
             GROUP BY source.zone_id,source.shard_id,source.first_start
         )
         INSERT INTO billing.hypervisor_allocation_windows
             (id,zone_id,shard_id,window_start,window_end)
         SELECT gen_random_uuid(),
                candidate.zone_id,candidate.shard_id,candidate.window_start,candidate.window_start + INTERVAL '1 hour'
         FROM candidates candidate
         WHERE candidate.window_start + INTERVAL '1 hour' <= $2
         ON CONFLICT (zone_id,shard_id,window_start,window_end) DO NOTHING",
    )
    .bind(i64::from(ALLOCATION_SHARD_COUNT))
    .bind(eligible_end)
    .execute(pg_pool)
    .await
    .map_err(|error| format!("plan Hypervisor allocation windows: {error}"))?;
    Ok(())
}

async fn claim_window(pg_pool: &PgPool) -> Result<Option<WindowClaim>, String> {
    let row = sqlx::query_as::<_, (Uuid, Uuid, i32, DateTime<Utc>, DateTime<Utc>, i32)>(
        "WITH candidate AS (
             SELECT id FROM billing.hypervisor_allocation_windows
             WHERE status='PENDING'
                OR (status='UNRATED' AND updated_at < NOW() - INTERVAL '30 seconds')
                OR (status='PROCESSING' AND updated_at < NOW() - INTERVAL '2 minutes')
             ORDER BY window_start,zone_id,shard_id
             FOR UPDATE SKIP LOCKED LIMIT 1
         )
         UPDATE billing.hypervisor_allocation_windows allocation_window
         SET status='PROCESSING',retry_count=retry_count+1,last_error=NULL,updated_at=NOW()
         FROM candidate WHERE allocation_window.id=candidate.id
         RETURNING allocation_window.id,allocation_window.zone_id,allocation_window.shard_id,allocation_window.window_start,allocation_window.window_end,allocation_window.retry_count",
    )
    .fetch_optional(pg_pool)
    .await
    .map_err(|error| format!("claim Hypervisor allocation window: {error}"))?;
    row.map(|(id, zone_id, shard_id, start, end, retry_count)| {
        Ok(WindowClaim {
            id,
            zone_id,
            shard_id,
            start,
            end,
            fencing_token: i64::from(retry_count),
        })
    })
    .transpose()
}

async fn settle_window(
    pg_pool: &PgPool,
    pricing_runtime: &Arc<PricingRuntime>,
    window: &WindowClaim,
) -> Result<(), String> {
    let intervals = sqlx::query_as::<_, (Uuid, i64, DateTime<Utc>, Option<DateTime<Utc>>, i64, i64, i64, i64)>(
        "SELECT resource_id,allocation_version,effective_from,effective_to,
                cpu_cores,memory_mib,disk_gib,gpu_count
         FROM billing.hypervisor_allocation_intervals
         WHERE zone_id=$1
           AND mod((hashtextextended(resource_id::text,0) & 9223372036854775807), $2::bigint)::int=$3
           AND effective_from < $5 AND (effective_to IS NULL OR $4 < effective_to)
         ORDER BY resource_id,allocation_version",
    )
    .bind(window.zone_id)
    .bind(i64::from(ALLOCATION_SHARD_COUNT))
    .bind(window.shard_id)
    .bind(window.start)
    .bind(window.end)
    .fetch_all(pg_pool)
    .await
    .map_err(|error| format!("read Hypervisor allocation intervals: {error}"))?
    .into_iter()
    .map(|row| AllocationInterval {
        resource_id: row.0,
        allocation_version: row.1,
        effective_from: row.2,
        effective_to: row.3,
        cpu_cores: row.4,
        memory_mib: row.5,
        disk_gib: row.6,
        gpu_count: row.7,
    })
    .collect::<Vec<_>>();

    let adjustment = resolve_zone_adjustment(pg_pool, window.zone_id, window.end).await?;
    let mut required_kinds = Vec::new();
    if intervals.iter().any(|interval| interval.cpu_cores > 0) {
        required_kinds.push(VCPU_KIND);
    }
    if intervals.iter().any(|interval| interval.memory_mib > 0) {
        required_kinds.push(MEMORY_KIND);
    }
    if intervals.iter().any(|interval| interval.disk_gib > 0) {
        required_kinds.push(DISK_KIND);
    }
    if intervals.iter().any(|interval| interval.gpu_count > 0) {
        required_kinds.push(GPU_KIND);
    }
    let mut leases = HashMap::new();
    for charge_kind in required_kinds {
        let lease = pricing_runtime
            .begin_billing_run(BillingRunCommand {
                source_module: "hypervisor",
                charge_kind_code: charge_kind,
                source_report_id: window.id,
                requested_start: window.start,
                requested_end: window.end,
                adjustment: adjustment.clone(),
                fencing_token: window.fencing_token,
            })
            .await
            .map_err(|error| error.to_string())?;
        if lease.snapshot.module_code != "hypervisor" {
            return Err(format!(
                "pricing kind {charge_kind} is not owned by Hypervisor"
            ));
        }
        leases.insert(charge_kind, lease);
    }

    let mut tx = pg_pool
        .begin()
        .await
        .map_err(|error| format!("begin Hypervisor allocation settlement: {error}"))?;
    let locked: (String, i32) = sqlx::query_as(
        "SELECT status,retry_count FROM billing.hypervisor_allocation_windows WHERE id=$1 FOR UPDATE",
    )
    .bind(window.id)
    .fetch_one(&mut *tx)
    .await
    .map_err(|error| format!("lock Hypervisor allocation window: {error}"))?;
    if locked.0 == "SETTLED" {
        tx.commit()
            .await
            .map_err(|error| format!("commit already settled Hypervisor window: {error}"))?;
        return Ok(());
    }
    if locked.0 != "PROCESSING" || i64::from(locked.1) != window.fencing_token {
        return Err(format!(
            "Hypervisor allocation window lost its fencing token: state={} token={}",
            locked.0, locked.1
        ));
    }

    let mut has_unrated = false;
    for interval in intervals {
        let segment_start = interval.effective_from.max(window.start);
        let segment_end = interval.effective_to.unwrap_or(window.end).min(window.end);
        let allocated_seconds = billed_seconds(segment_start, segment_end)?;
        if allocated_seconds == 0 {
            continue;
        }
        let components = [
            ComponentLine {
                component: "VCPU",
                charge_kind: VCPU_KIND,
                usage_unit: "CORE_SECOND",
                limit: interval.cpu_cores,
            },
            ComponentLine {
                component: "MEMORY",
                charge_kind: MEMORY_KIND,
                usage_unit: "MIB_SECOND",
                limit: interval.memory_mib,
            },
            ComponentLine {
                component: "DISK",
                charge_kind: DISK_KIND,
                usage_unit: "GIB_SECOND",
                limit: interval.disk_gib,
            },
            ComponentLine {
                component: "GPU",
                charge_kind: GPU_KIND,
                usage_unit: "GPU_SECOND",
                limit: interval.gpu_count,
            },
        ];
        for component in components
            .into_iter()
            .filter(|component| component.limit > 0)
        {
            let quantity = component
                .limit
                .checked_mul(allocated_seconds)
                .ok_or_else(|| {
                    format!("{} allocation quantity exceeds BIGINT", component.component)
                })?;
            let pricing = leases
                .get(component.charge_kind)
                .ok_or_else(|| format!("missing pricing lease for {}", component.charge_kind))?;
            if pricing.snapshot.raw_input_unit != component.usage_unit {
                return Err(format!(
                    "pricing unit mismatch for {}",
                    component.charge_kind
                ));
            }
            let line_id = Uuid::new_v5(
                &LINE_NAMESPACE,
                format!(
                    "{}:{}:{}:{}",
                    window.id,
                    interval.resource_id,
                    interval.allocation_version,
                    component.component
                )
                .as_bytes(),
            );
            let evidence_hash = allocation_evidence_hash(
                window,
                &interval,
                component.component,
                quantity,
                segment_start,
                segment_end,
            );
            sqlx::query(
                "INSERT INTO billing.hypervisor_allocation_lines
                 (id,window_id,resource_id,zone_id,allocation_version,component,usage_quantity,usage_unit,
                  charge_kind_code,usage_settlement_run_id,pricing_schedule_version_id,pricing_checksum,source_evidence_hash)
                 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
                 ON CONFLICT (window_id,resource_id,allocation_version,component) DO NOTHING",
            )
            .bind(line_id)
            .bind(window.id)
            .bind(interval.resource_id)
            .bind(window.zone_id)
            .bind(interval.allocation_version)
            .bind(component.component)
            .bind(quantity)
            .bind(component.usage_unit)
            .bind(component.charge_kind)
            .bind(pricing.billing_run_id)
            .bind(pricing.snapshot.version_id)
            .bind(&pricing.snapshot.checksum)
            .bind(&evidence_hash)
            .execute(&mut *tx)
            .await
            .map_err(|error| format!("insert Hypervisor allocation line: {error}"))?;

            let line_status: String = sqlx::query_scalar(
                "SELECT status FROM billing.hypervisor_allocation_lines WHERE id=$1 FOR UPDATE",
            )
            .bind(line_id)
            .fetch_one(&mut *tx)
            .await
            .map_err(|error| format!("lock Hypervisor allocation line: {error}"))?;
            if line_status == "SETTLED" {
                continue;
            }
            let owners = sqlx::query_as::<Postgres, (Uuid, String)>(
                "SELECT owner_id,owner_type::text
                 FROM billing.resource_ownership_projection
                 WHERE resource_type='HYPERVISOR_VM' AND resource_id=$1 AND zone_id=$2
                   AND effective_from < $4 AND (effective_to IS NULL OR $3 < effective_to)
                 ORDER BY ownership_version DESC LIMIT 2",
            )
            .bind(interval.resource_id)
            .bind(window.zone_id)
            .bind(segment_start)
            .bind(segment_end)
            .fetch_all(&mut *tx)
            .await
            .map_err(|error| format!("resolve Hypervisor owner: {error}"))?;
            if owners.len() != 1 {
                has_unrated = true;
                let reason = if owners.is_empty() {
                    "OWNER_PROJECTION_MISSING"
                } else {
                    "OWNER_PROJECTION_AMBIGUOUS"
                };
                persist_unrated(
                    &mut tx,
                    AllocationUnratedLine {
                        line_id,
                        window,
                        resource_id: interval.resource_id,
                        quantity,
                        charge_kind: component.charge_kind,
                        usage_unit: component.usage_unit,
                        pricing_version_id: pricing.snapshot.version_id,
                        evidence_hash: &evidence_hash,
                        reason,
                    },
                )
                .await?;
                continue;
            }
            let (owner_id, owner_type) = &owners[0];
            let amount = pricing
                .snapshot
                .charge_micro_units(
                    u64::try_from(quantity)
                        .map_err(|_| "negative allocation quantity".to_string())?,
                    pricing.adjustment.as_ref(),
                )
                .map_err(|error| error.to_string())?;
            if amount <= 0 {
                sqlx::query(
                    "UPDATE billing.hypervisor_allocation_lines
                     SET amount_micro_units=0,owner_id=$1,owner_type=$2::billing.owner_type,status='SETTLED',settled_at=NOW()
                     WHERE id=$3",
                )
                .bind(owner_id)
                .bind(owner_type)
                .bind(line_id)
                .execute(&mut *tx)
                .await
                .map_err(|error| format!("settle zero-cost Hypervisor line: {error}"))?;
                continue;
            }
            let reference = format!(
                "hypervisor-allocation:{}:{}:{}",
                window.id, interval.resource_id, component.component
            );
            let description = format!(
                "Hypervisor {} allocated quantity {} in UTC window {}",
                component.component, quantity, window.id
            );
            match settle_usage_charge(
                &mut tx,
                UsageChargeCommand {
                    ledger_entry_id: line_id,
                    owner_id: *owner_id,
                    owner_type,
                    amount_micro_units: amount,
                    module_code: "hypervisor",
                    charge_kind_code: component.charge_kind,
                    reference_id: &reference,
                    description: &description,
                    usage_settlement_run_id: pricing.billing_run_id,
                    pricing_schedule_id: pricing.snapshot.pricing_schedule_id,
                    pricing_schedule_version_id: pricing.snapshot.version_id,
                    pricing_checksum: &pricing.snapshot.checksum,
                    resource_id: interval.resource_id,
                    resource_type: "HYPERVISOR_VM",
                    usage_quantity: quantity,
                    usage_unit: component.usage_unit,
                    occurred_at: window.end,
                    source_evidence_hash: &evidence_hash,
                },
            )
            .await?
            {
                UsageChargeOutcome::Settled => {
                    sqlx::query(
                        "UPDATE billing.hypervisor_allocation_lines
                         SET amount_micro_units=$1,owner_id=$2,owner_type=$3::billing.owner_type,status='SETTLED',settled_at=NOW(),reason=NULL
                         WHERE id=$4",
                    )
                    .bind(amount)
                    .bind(owner_id)
                    .bind(owner_type)
                    .bind(line_id)
                    .execute(&mut *tx)
                    .await
                    .map_err(|error| format!("mark Hypervisor allocation line settled: {error}"))?;
                }
                UsageChargeOutcome::Unrated(reason) => {
                    has_unrated = true;
                    persist_unrated(
                        &mut tx,
                        AllocationUnratedLine {
                            line_id,
                            window,
                            resource_id: interval.resource_id,
                            quantity,
                            charge_kind: component.charge_kind,
                            usage_unit: component.usage_unit,
                            pricing_version_id: pricing.snapshot.version_id,
                            evidence_hash: &evidence_hash,
                            reason,
                        },
                    )
                    .await?;
                }
            }
        }
    }

    sqlx::query(
        "UPDATE billing.unrated_usage evidence SET status='RESOLVED',last_error=NULL,updated_at=NOW()
         FROM billing.hypervisor_allocation_lines line
         WHERE evidence.id=line.id AND line.window_id=$1 AND line.status='SETTLED' AND evidence.status <> 'RESOLVED'",
    )
    .bind(window.id)
    .execute(&mut *tx)
    .await
    .map_err(|error| format!("resolve Hypervisor unrated evidence: {error}"))?;
    let unrated: i64 = sqlx::query_scalar(
        "SELECT COUNT(*) FROM billing.hypervisor_allocation_lines WHERE window_id=$1 AND status <> 'SETTLED'",
    )
    .bind(window.id)
    .fetch_one(&mut *tx)
    .await
    .map_err(|error| format!("read Hypervisor allocation line status: {error}"))?;
    let final_status = if has_unrated || unrated > 0 {
        "UNRATED"
    } else {
        "SETTLED"
    };
    sqlx::query(
        "UPDATE billing.hypervisor_allocation_windows
         SET status=$1,last_error=NULL,updated_at=NOW(),settled_at=CASE WHEN $1='SETTLED' THEN NOW() ELSE settled_at END
         WHERE id=$2",
    )
    .bind(final_status)
    .bind(window.id)
    .execute(&mut *tx)
    .await
    .map_err(|error| format!("complete Hypervisor allocation window: {error}"))?;
    tx.commit()
        .await
        .map_err(|error| format!("commit Hypervisor allocation settlement: {error}"))?;
    if final_status == "SETTLED" {
        for lease in leases.values() {
            pricing_runtime
                .complete_billing_run(lease.billing_run_id, window.end, window.fencing_token)
                .await
                .map_err(|error| error.to_string())?;
        }
        Ok(())
    } else {
        Err("Hypervisor allocation window retains unrated lines".to_string())
    }
}

async fn resolve_zone_adjustment(
    pg_pool: &PgPool,
    zone_id: Uuid,
    at: DateTime<Utc>,
) -> Result<Option<RateAdjustmentSnapshot>, String> {
    let row = sqlx::query_as::<_, (Uuid, i32, DateTime<Utc>, i64, i64, String)>(
        "SELECT id,version_number,effective_from,multiplier_numerator,multiplier_denominator,checksum
         FROM billing.hypervisor_zone_price_adjustment_versions
         WHERE zone_id=$1 AND status <> 'CANCELLED'
           AND effective_from <= $2 AND (effective_to IS NULL OR $2 < effective_to)
         ORDER BY version_number DESC LIMIT 1",
    )
    .bind(zone_id)
    .bind(at)
    .fetch_optional(pg_pool)
    .await
    .map_err(|error| format!("resolve Hypervisor Zone adjustment: {error}"))?;
    let Some((id, version_number, effective_from, numerator, denominator, checksum)) = row else {
        return Ok(None);
    };
    if checksum
        != zone_adjustment_checksum(
            zone_id,
            version_number,
            effective_from,
            numerator,
            denominator,
        )
    {
        return Err(format!("Hypervisor Zone adjustment {id} checksum mismatch"));
    }
    Ok(Some(RateAdjustmentSnapshot {
        id,
        version_number,
        checksum,
        numerator,
        denominator,
    }))
}

async fn persist_unrated(
    tx: &mut sqlx::Transaction<'_, Postgres>,
    line: AllocationUnratedLine<'_>,
) -> Result<(), String> {
    sqlx::query(
        "INSERT INTO billing.unrated_usage
         (id,module_code,charge_kind_code,resource_type,resource_id,resource_name,metering_hour,
          usage_quantity,usage_unit,reason,source_report_id,source_evidence_hash,pricing_schedule_version_id)
         VALUES ($1,'hypervisor',$2,'HYPERVISOR_VM',$3,$4,$5,$6,$7,$8,$9,$10,$11)
         ON CONFLICT (id) DO UPDATE SET reason=EXCLUDED.reason,status='PENDING',updated_at=NOW()",
    )
    .bind(line.line_id)
    .bind(line.charge_kind)
    .bind(line.resource_id)
    .bind(line.resource_id.to_string())
    .bind(line.window.end)
    .bind(line.quantity)
    .bind(line.usage_unit)
    .bind(line.reason)
    .bind(line.window.id)
    .bind(line.evidence_hash)
    .bind(line.pricing_version_id)
    .execute(&mut **tx)
    .await
    .map_err(|error| format!("persist unrated Hypervisor evidence: {error}"))?;
    sqlx::query(
        "UPDATE billing.hypervisor_allocation_lines SET status='UNRATED',reason=$1 WHERE id=$2",
    )
    .bind(line.reason)
    .bind(line.line_id)
    .execute(&mut **tx)
    .await
    .map_err(|error| format!("mark Hypervisor line unrated: {error}"))?;
    Ok(())
}

fn allocation_evidence_hash(
    window: &WindowClaim,
    interval: &AllocationInterval,
    component: &str,
    quantity: i64,
    segment_start: DateTime<Utc>,
    segment_end: DateTime<Utc>,
) -> String {
    let mut hasher = Sha256::new();
    for value in [
        window.id.to_string(),
        interval.resource_id.to_string(),
        interval.allocation_version.to_string(),
        component.to_string(),
        quantity.to_string(),
        segment_start.to_rfc3339_opts(chrono::SecondsFormat::Millis, true),
        segment_end.to_rfc3339_opts(chrono::SecondsFormat::Millis, true),
    ] {
        hasher.update((value.len() as u64).to_be_bytes());
        hasher.update(value.as_bytes());
    }
    format!("{:x}", hasher.finalize())
}

fn billed_seconds(start: DateTime<Utc>, end: DateTime<Utc>) -> Result<i64, String> {
    let duration_ms = end.signed_duration_since(start).num_milliseconds();
    if duration_ms <= 0 {
        return Ok(0);
    }
    duration_ms
        .checked_add(999)
        .map(|milliseconds| milliseconds / 1_000)
        .ok_or_else(|| "allocated duration overflow".to_string())
}

async fn wait_or_shutdown(duration: Duration, shutdown_rx: &mut watch::Receiver<bool>) {
    tokio::select! {
        _ = tokio::time::sleep(duration) => {}
        _ = shutdown_rx.changed() => {}
    }
}

#[cfg(test)]
mod tests {
    use super::billed_seconds;
    use chrono::{TimeZone, Utc};

    #[test]
    fn allocation_precision_is_seconds_without_a_per_second_tick() {
        let start = Utc.timestamp_millis_opt(1_700_000_000_250).unwrap();
        assert_eq!(
            billed_seconds(start, start + chrono::Duration::milliseconds(1)).unwrap(),
            1
        );
        assert_eq!(
            billed_seconds(start, start + chrono::Duration::milliseconds(1_000)).unwrap(),
            1
        );
        assert_eq!(
            billed_seconds(start, start + chrono::Duration::milliseconds(3_600_000)).unwrap(),
            3_600
        );
        assert_eq!(billed_seconds(start, start).unwrap(), 0);
    }
}
