use std::sync::Arc;
use std::time::Duration;

use chrono::{DateTime, Utc};
use sqlx::{PgPool, Postgres};
use tokio::sync::watch;
use uuid::Uuid;

use crate::engine::{
    BillingRunCommand, PricingRuntime, RateAdjustmentSnapshot, UsageChargeCommand,
    UsageChargeOutcome, settle_usage_charge,
};

use super::network_usage_proto::HypervisorNetworkUsageReportV1;
use super::network_usage_stream::{
    GROUP, STREAM, acknowledge, decode_report, next_entries, quarantine, wait_or_shutdown,
};
use super::zone_adjustment_checksum;

const NETWORK_IN_KIND: &str = "hypervisor.network_in.byte";
const NETWORK_OUT_KIND: &str = "hypervisor.network_out.byte";
const REPORT_ID_CHECKSUM_CONFLICT: &str = "HYPERVISOR_NETWORK_REPORT_ID_CHECKSUM_CONFLICT";
const NETWORK_IN_NAMESPACE: Uuid = Uuid::from_u128(0x334c_4562_ef6e_5df5_aad8_7037_c8c3_d20a);
const NETWORK_OUT_NAMESPACE: Uuid = Uuid::from_u128(0x863e_d99d_4cbc_5202_b3cc_4ffd_d8ee_902b);

struct NetworkUnratedLine<'a> {
    line_id: Uuid,
    report_id: Uuid,
    resource_id: Uuid,
    metering_hour: DateTime<Utc>,
    quantity: i64,
    charge_kind: &'a str,
    evidence_hash: &'a str,
    pricing_version_id: Uuid,
    reason: &'a str,
}

pub async fn run_hypervisor_network_usage_settlement(
    pg_pool: PgPool,
    mut redis_conn: redis::aio::MultiplexedConnection,
    pricing_runtime: Arc<PricingRuntime>,
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
        eprintln!("Hypervisor network consumer cannot create Redis group: {error}");
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
                eprintln!("Hypervisor network stream read failed: {error}");
                wait_or_shutdown(Duration::from_secs(2), &mut shutdown_rx).await;
                continue;
            }
        };
        for entry in entries {
            let Some(payload) = entry.payload.as_deref() else {
                let _ = quarantine(
                    &mut redis_conn,
                    &entry.id,
                    "HYPERVISOR_NETWORK_PAYLOAD_MISSING",
                )
                .await;
                continue;
            };
            let report = match decode_report(payload) {
                Ok(report) => report,
                Err(error) if error == REPORT_ID_CHECKSUM_CONFLICT => {
                    let _ =
                        quarantine(&mut redis_conn, &entry.id, REPORT_ID_CHECKSUM_CONFLICT).await;
                    continue;
                }
                Err(error) => {
                    let _ = quarantine(&mut redis_conn, &entry.id, error).await;
                    continue;
                }
            };
            if entry.report_id.as_deref() != Some(report.report_id.as_str())
                || entry.zone_id.as_deref() != Some(report.zone_id.as_str())
                || entry.resource_id.as_deref() != Some(report.resource_id.as_str())
                || entry.report_sha256.as_deref() != Some(report.report_sha256.as_slice())
            {
                let _ = quarantine(
                    &mut redis_conn,
                    &entry.id,
                    "HYPERVISOR_NETWORK_STREAM_FIELDS_MISMATCH",
                )
                .await;
                continue;
            }
            match settle_report(&pg_pool, &pricing_runtime, &report, payload).await {
                Ok(()) => {
                    if let Err(error) = acknowledge(&mut redis_conn, &entry.id).await {
                        eprintln!("Hypervisor network ACK failed: {error}");
                        break;
                    }
                }
                Err(error) => {
                    eprintln!("Hypervisor network report remains pending: {error}");
                    wait_or_shutdown(Duration::from_secs(1), &mut shutdown_rx).await;
                    break;
                }
            }
        }
    }
}

async fn settle_report(
    pg_pool: &PgPool,
    pricing_runtime: &Arc<PricingRuntime>,
    report: &HypervisorNetworkUsageReportV1,
    payload: &[u8],
) -> Result<(), String> {
    let report_id =
        Uuid::parse_str(&report.report_id).map_err(|_| "invalid report UUID".to_string())?;
    let zone_id = Uuid::parse_str(&report.zone_id).map_err(|_| "invalid Zone UUID".to_string())?;
    let resource_id =
        Uuid::parse_str(&report.resource_id).map_err(|_| "invalid resource UUID".to_string())?;
    let window_start = DateTime::<Utc>::from_timestamp_millis(report.window_start_unix_ms)
        .ok_or_else(|| "invalid report window start".to_string())?;
    let window_end = DateTime::<Utc>::from_timestamp_millis(report.window_end_unix_ms)
        .ok_or_else(|| "invalid report window end".to_string())?;
    let adjustment = resolve_zone_adjustment(pg_pool, zone_id, window_end).await?;
    let fencing_token = i64::try_from(report.sequence)
        .map_err(|_| "Hypervisor network sequence exceeds BIGINT".to_string())?;
    let mut pricing: Vec<(&str, crate::engine::BillingPricingLease)> = Vec::new();
    for (kind, quantity) in [
        (NETWORK_IN_KIND, report.network_in_bytes),
        (NETWORK_OUT_KIND, report.network_out_bytes),
    ] {
        if quantity == 0 {
            continue;
        }
        let lease = match pricing_runtime
            .begin_billing_run(BillingRunCommand {
                source_module: "hypervisor",
                charge_kind_code: kind,
                source_report_id: report_id,
                requested_start: window_start,
                requested_end: window_end,
                adjustment: adjustment.clone(),
                fencing_token,
            })
            .await
        {
            Ok(lease) => lease,
            Err(error) => {
                for (_, started) in &pricing {
                    let _ = pricing_runtime
                        .mark_billing_run_retrying(started.billing_run_id, fencing_token)
                        .await;
                }
                return Err(error.to_string());
            }
        };
        if lease.snapshot.module_code != "hypervisor"
            || lease.snapshot.charge_kind_code != kind
            || lease.snapshot.raw_input_unit != "BYTE"
        {
            return Err(format!(
                "incompatible Hypervisor pricing snapshot for {kind}"
            ));
        }
        pricing.push((kind, lease));
    }

    let evidence_hash = report
        .report_sha256
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>();
    let mut tx = pg_pool
        .begin()
        .await
        .map_err(|error| format!("begin Hypervisor network settlement: {error}"))?;
    sqlx::query(
        "INSERT INTO billing.hypervisor_network_usage_report_inbox
         (report_id,zone_id,resource_id,window_start,window_end,sequence,payload_sha256,payload,status)
         VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'PROCESSING') ON CONFLICT (report_id) DO NOTHING",
    )
    .bind(report_id)
    .bind(zone_id)
    .bind(resource_id)
    .bind(window_start)
    .bind(window_end)
    .bind(fencing_token)
    .bind(report.report_sha256.as_slice())
    .bind(payload)
    .execute(&mut *tx)
    .await
    .map_err(|error| format!("insert Hypervisor network report inbox: {error}"))?;
    let existing: (Vec<u8>, String) = sqlx::query_as(
        "SELECT payload_sha256,status FROM billing.hypervisor_network_usage_report_inbox WHERE report_id=$1 FOR UPDATE",
    )
    .bind(report_id)
    .fetch_one(&mut *tx)
    .await
    .map_err(|error| format!("lock Hypervisor network report inbox: {error}"))?;
    if existing.0 != report.report_sha256 {
        let _ = tx.rollback().await;
        for (_, lease) in &pricing {
            let _ = pricing_runtime
                .mark_billing_run_retrying(lease.billing_run_id, fencing_token)
                .await;
        }
        return Err(REPORT_ID_CHECKSUM_CONFLICT.to_string());
    }
    if existing.1 == "SETTLED" {
        tx.commit()
            .await
            .map_err(|error| format!("commit duplicate report: {error}"))?;
        for (_, lease) in &pricing {
            pricing_runtime
                .complete_billing_run(lease.billing_run_id, window_end, fencing_token)
                .await
                .map_err(|error| error.to_string())?;
        }
        return Ok(());
    }
    sqlx::query(
        "UPDATE billing.hypervisor_network_usage_report_inbox
         SET status='PROCESSING',retry_count=retry_count+1,last_error=NULL WHERE report_id=$1",
    )
    .bind(report_id)
    .execute(&mut *tx)
    .await
    .map_err(|error| format!("prepare Hypervisor network report replay: {error}"))?;

    let owners = sqlx::query_as::<Postgres, (Uuid, String)>(
        "SELECT owner_id,owner_type::text FROM billing.resource_ownership_projection
         WHERE resource_type='HYPERVISOR_VM' AND resource_id=$1 AND zone_id=$2
           AND effective_from < $4 AND (effective_to IS NULL OR $3 < effective_to)
         ORDER BY ownership_version DESC LIMIT 2",
    )
    .bind(resource_id)
    .bind(zone_id)
    .bind(window_start)
    .bind(window_end)
    .fetch_all(&mut *tx)
    .await
    .map_err(|error| format!("resolve Hypervisor network owner: {error}"))?;
    let mut has_unrated = false;
    for (kind, raw_quantity, direction, namespace) in [
        (
            NETWORK_IN_KIND,
            report.network_in_bytes,
            "NETWORK_IN",
            &NETWORK_IN_NAMESPACE,
        ),
        (
            NETWORK_OUT_KIND,
            report.network_out_bytes,
            "NETWORK_OUT",
            &NETWORK_OUT_NAMESPACE,
        ),
    ] {
        if raw_quantity == 0 {
            continue;
        }
        let quantity = i64::try_from(raw_quantity)
            .map_err(|_| "Hypervisor network quantity exceeds BIGINT".to_string())?;
        let lease = pricing
            .iter()
            .find(|(candidate, _)| *candidate == kind)
            .map(|(_, lease)| lease)
            .ok_or_else(|| format!("missing Hypervisor network pricing lease for {kind}"))?;
        let line_id = Uuid::new_v5(namespace, format!("{report_id}:{direction}").as_bytes());
        sqlx::query(
            "INSERT INTO billing.hypervisor_network_usage_lines
             (id,report_id,resource_id,zone_id,direction,usage_quantity,usage_unit,charge_kind_code,
              usage_settlement_run_id,pricing_schedule_version_id,pricing_checksum,source_evidence_hash)
             VALUES ($1,$2,$3,$4,$5,$6,'BYTE',$7,$8,$9,$10,$11)
             ON CONFLICT (report_id,direction) DO NOTHING",
        )
        .bind(line_id)
        .bind(report_id)
        .bind(resource_id)
        .bind(zone_id)
        .bind(direction)
        .bind(quantity)
        .bind(kind)
        .bind(lease.billing_run_id)
        .bind(lease.snapshot.version_id)
        .bind(&lease.snapshot.checksum)
        .bind(&evidence_hash)
        .execute(&mut *tx)
        .await
        .map_err(|error| format!("insert Hypervisor network line: {error}"))?;
        let status: String = sqlx::query_scalar(
            "SELECT status FROM billing.hypervisor_network_usage_lines WHERE id=$1 FOR UPDATE",
        )
        .bind(line_id)
        .fetch_one(&mut *tx)
        .await
        .map_err(|error| format!("lock Hypervisor network line: {error}"))?;
        if status == "SETTLED" {
            continue;
        }
        if owners.len() != 1 {
            has_unrated = true;
            let reason = if owners.is_empty() {
                "OWNER_PROJECTION_MISSING"
            } else {
                "OWNER_PROJECTION_AMBIGUOUS"
            };
            persist_unrated(
                &mut tx,
                NetworkUnratedLine {
                    line_id,
                    report_id,
                    resource_id,
                    metering_hour: window_end,
                    quantity,
                    charge_kind: kind,
                    evidence_hash: &evidence_hash,
                    pricing_version_id: lease.snapshot.version_id,
                    reason,
                },
            )
            .await?;
            continue;
        }
        let (owner_id, owner_type) = &owners[0];
        let amount = lease
            .snapshot
            .charge_micro_units(raw_quantity, lease.adjustment.as_ref())
            .map_err(|error| error.to_string())?;
        if amount > 0 {
            let reference = format!("hypervisor-network:{report_id}:{direction}");
            let description =
                format!("Hypervisor {direction} quantity {quantity} in report {report_id}");
            if let UsageChargeOutcome::Unrated(reason) = settle_usage_charge(
                &mut tx,
                UsageChargeCommand {
                    ledger_entry_id: line_id,
                    owner_id: *owner_id,
                    owner_type,
                    amount_micro_units: amount,
                    module_code: "hypervisor",
                    charge_kind_code: kind,
                    reference_id: &reference,
                    description: &description,
                    usage_settlement_run_id: lease.billing_run_id,
                    pricing_schedule_id: lease.snapshot.pricing_schedule_id,
                    pricing_schedule_version_id: lease.snapshot.version_id,
                    pricing_checksum: &lease.snapshot.checksum,
                    resource_id,
                    resource_type: "HYPERVISOR_VM",
                    usage_quantity: quantity,
                    usage_unit: "BYTE",
                    occurred_at: window_end,
                    source_evidence_hash: &evidence_hash,
                },
            )
            .await?
            {
                has_unrated = true;
                persist_unrated(
                    &mut tx,
                    NetworkUnratedLine {
                        line_id,
                        report_id,
                        resource_id,
                        metering_hour: window_end,
                        quantity,
                        charge_kind: kind,
                        evidence_hash: &evidence_hash,
                        pricing_version_id: lease.snapshot.version_id,
                        reason,
                    },
                )
                .await?;
                continue;
            }
        }
        sqlx::query(
            "UPDATE billing.hypervisor_network_usage_lines
             SET amount_micro_units=$1,owner_id=$2,owner_type=$3::billing.owner_type,status='SETTLED',reason=NULL,settled_at=NOW()
             WHERE id=$4",
        )
        .bind(amount)
        .bind(owner_id)
        .bind(owner_type)
        .bind(line_id)
        .execute(&mut *tx)
        .await
        .map_err(|error| format!("settle Hypervisor network line: {error}"))?;
    }
    sqlx::query(
        "UPDATE billing.unrated_usage usage
         SET status='RESOLVED',last_error=NULL,updated_at=NOW()
         FROM billing.hypervisor_network_usage_lines line
         WHERE usage.id=line.id AND line.report_id=$1 AND line.status='SETTLED'
           AND usage.status <> 'RESOLVED'",
    )
    .bind(report_id)
    .execute(&mut *tx)
    .await
    .map_err(|error| format!("resolve replayed Hypervisor network evidence: {error}"))?;
    let report_status = if has_unrated { "UNRATED" } else { "SETTLED" };
    sqlx::query(
        "UPDATE billing.hypervisor_network_usage_report_inbox
         SET status=$1,settled_at=NOW(),last_error=NULL WHERE report_id=$2",
    )
    .bind(report_status)
    .bind(report_id)
    .execute(&mut *tx)
    .await
    .map_err(|error| format!("complete Hypervisor network report: {error}"))?;
    tx.commit()
        .await
        .map_err(|error| format!("commit Hypervisor network report: {error}"))?;
    if has_unrated {
        for (_, lease) in &pricing {
            let _ = pricing_runtime
                .mark_billing_run_retrying(lease.billing_run_id, fencing_token)
                .await;
        }
        return Err("Hypervisor network report retains unrated lines".to_string());
    }
    for (_, lease) in &pricing {
        pricing_runtime
            .complete_billing_run(lease.billing_run_id, window_end, fencing_token)
            .await
            .map_err(|error| error.to_string())?;
    }
    Ok(())
}

async fn persist_unrated(
    tx: &mut sqlx::Transaction<'_, Postgres>,
    line: NetworkUnratedLine<'_>,
) -> Result<(), String> {
    sqlx::query(
        "INSERT INTO billing.unrated_usage
         (id,module_code,charge_kind_code,resource_type,resource_id,resource_name,metering_hour,
          usage_quantity,usage_unit,reason,source_report_id,source_evidence_hash,pricing_schedule_version_id)
         VALUES ($1,'hypervisor',$2,'HYPERVISOR_VM',$3,$4,$5,$6,'BYTE',$7,$8,$9,$10)
         ON CONFLICT (id) DO UPDATE SET status='PENDING',retry_count=billing.unrated_usage.retry_count+1,
           reason=EXCLUDED.reason,updated_at=NOW()",
    )
    .bind(line.line_id)
    .bind(line.charge_kind)
    .bind(line.resource_id)
    .bind(line.resource_id.to_string())
    .bind(line.metering_hour)
    .bind(line.quantity)
    .bind(line.reason)
    .bind(line.report_id)
    .bind(line.evidence_hash)
    .bind(line.pricing_version_id)
    .execute(&mut **tx)
    .await
    .map_err(|error| format!("persist unrated Hypervisor network line: {error}"))?;
    sqlx::query(
        "UPDATE billing.hypervisor_network_usage_lines SET status='UNRATED',reason=$1,settled_at=NOW() WHERE id=$2",
    )
    .bind(line.reason)
    .bind(line.line_id)
    .execute(&mut **tx)
    .await
    .map_err(|error| format!("mark Hypervisor network line unrated: {error}"))?;
    Ok(())
}

async fn resolve_zone_adjustment(
    pg_pool: &PgPool,
    zone_id: Uuid,
    at: DateTime<Utc>,
) -> Result<Option<RateAdjustmentSnapshot>, String> {
    let row = sqlx::query_as::<Postgres, (Uuid, i32, DateTime<Utc>, i64, i64, String)>(
        "SELECT id,version_number,effective_from,multiplier_numerator,multiplier_denominator,checksum
         FROM billing.hypervisor_zone_price_adjustment_versions
         WHERE zone_id=$1 AND status <> 'CANCELLED' AND effective_from <= $2
           AND (effective_to IS NULL OR $2 < effective_to)
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
