use std::sync::Arc;
use std::time::Duration;

use chrono::{DateTime, TimeDelta, Utc};
use sqlx::{PgPool, Postgres};
use tokio::sync::watch;
use uuid::Uuid;

use crate::engine::{
    BillingRunCommand, PricingRuntime, RateAdjustmentSnapshot, UsageChargeCommand,
    UsageChargeOutcome, settle_usage_charge,
};

use super::accepted_usage_proto::MailAcceptedUsageV1;
use super::accepted_usage_stream::{
    GROUP, STREAM, acknowledge, decode_evidence, next_entries, quarantine, wait_or_shutdown,
};
use super::zone_adjustment_checksum;

const CHARGE_KIND: &str = "mail.delivery.accepted_recipient";
const EVIDENCE_CHECKSUM_CONFLICT: &str = "MAIL_ACCEPTED_USAGE_ID_CHECKSUM_CONFLICT";
const LEDGER_NAMESPACE: Uuid = Uuid::from_u128(0x448e_225c_1f6b_5a3f_b9a4_ee5f_4bf3_c611);

struct MailUnratedLine<'a> {
    line_id: Uuid,
    evidence_id: Uuid,
    resource_id: Uuid,
    accepted_at: DateTime<Utc>,
    evidence_hash: &'a str,
    pricing_version_id: Uuid,
    reason: &'a str,
}

pub async fn run_mail_accepted_usage_settlement(
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
        eprintln!("Mail accepted usage consumer cannot create Redis group: {error}");
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
                eprintln!("Mail accepted usage stream read failed: {error}");
                wait_or_shutdown(Duration::from_secs(2), &mut shutdown_rx).await;
                continue;
            }
        };
        for entry in entries {
            let Some(payload) = entry.payload.as_deref() else {
                let _ = quarantine(
                    &mut redis_conn,
                    &entry.id,
                    "MAIL_ACCEPTED_USAGE_PAYLOAD_MISSING",
                )
                .await;
                continue;
            };
            let evidence = match decode_evidence(payload) {
                Ok(evidence) => evidence,
                Err(error) => {
                    let _ = quarantine(&mut redis_conn, &entry.id, error).await;
                    continue;
                }
            };
            if entry.evidence_id.as_deref() != Some(evidence.evidence_id.as_str())
                || entry.zone_id.as_deref() != Some(evidence.zone_id.as_str())
                || entry.resource_id.as_deref() != Some(evidence.resource_id.as_str())
                || entry.evidence_sha256.as_deref() != Some(evidence.evidence_sha256.as_slice())
            {
                let _ = quarantine(
                    &mut redis_conn,
                    &entry.id,
                    "MAIL_ACCEPTED_USAGE_STREAM_FIELDS_MISMATCH",
                )
                .await;
                continue;
            }
            match settle_evidence(&pg_pool, &pricing_runtime, &evidence, payload).await {
                Ok(()) => {
                    if let Err(error) = acknowledge(&mut redis_conn, &entry.id).await {
                        eprintln!("Mail accepted usage ACK failed: {error}");
                        break;
                    }
                }
                Err(error) if error == EVIDENCE_CHECKSUM_CONFLICT => {
                    let _ = quarantine(&mut redis_conn, &entry.id, &error).await;
                }
                Err(error) => {
                    eprintln!("Mail accepted usage remains pending: {error}");
                    wait_or_shutdown(Duration::from_secs(1), &mut shutdown_rx).await;
                    break;
                }
            }
        }
    }
}

async fn settle_evidence(
    pg_pool: &PgPool,
    pricing_runtime: &Arc<PricingRuntime>,
    evidence: &MailAcceptedUsageV1,
    payload: &[u8],
) -> Result<(), String> {
    let evidence_id =
        Uuid::parse_str(&evidence.evidence_id).map_err(|_| "invalid evidence UUID".to_string())?;
    let zone_id =
        Uuid::parse_str(&evidence.zone_id).map_err(|_| "invalid Zone UUID".to_string())?;
    let resource_id = Uuid::parse_str(&evidence.resource_id)
        .map_err(|_| "invalid Mail consumer UUID".to_string())?;
    let accepted_at = DateTime::<Utc>::from_timestamp_millis(evidence.accepted_at_unix_ms)
        .ok_or_else(|| "invalid accepted timestamp".to_string())?;
    let window_start = accepted_at
        .checked_sub_signed(TimeDelta::milliseconds(1))
        .ok_or_else(|| "invalid Mail pricing boundary".to_string())?;
    let fencing_token = evidence.accepted_at_unix_ms;
    let adjustment = resolve_zone_adjustment(pg_pool, zone_id, accepted_at).await?;
    let pricing = pricing_runtime
        .begin_billing_run(BillingRunCommand {
            source_module: "mail",
            charge_kind_code: CHARGE_KIND,
            source_report_id: evidence_id,
            requested_start: window_start,
            requested_end: accepted_at,
            adjustment,
            fencing_token,
        })
        .await
        .map_err(|error| error.to_string())?;
    if pricing.snapshot.module_code != "mail"
        || pricing.snapshot.charge_kind_code != CHARGE_KIND
        || pricing.snapshot.raw_input_unit != "RECIPIENT"
    {
        return Err("incompatible Mail accepted usage pricing snapshot".to_string());
    }

    let evidence_hash = evidence
        .evidence_sha256
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>();
    let ledger_id = Uuid::new_v5(&LEDGER_NAMESPACE, evidence_id.as_bytes());
    let mut tx = pg_pool
        .begin()
        .await
        .map_err(|error| format!("begin Mail accepted usage settlement: {error}"))?;
    sqlx::query(
        "INSERT INTO billing.mail_accepted_usage_inbox
         (evidence_id,zone_id,resource_id,accepted_at,recipient_quantity,payload_sha256,payload,
          charge_kind_code,usage_settlement_run_id,pricing_schedule_version_id,pricing_checksum,status)
         VALUES ($1,$2,$3,$4,1,$5,$6,$7,$8,$9,$10,'PROCESSING')
         ON CONFLICT (evidence_id) DO NOTHING",
    )
    .bind(evidence_id)
    .bind(zone_id)
    .bind(resource_id)
    .bind(accepted_at)
    .bind(evidence.evidence_sha256.as_slice())
    .bind(payload)
    .bind(CHARGE_KIND)
    .bind(pricing.billing_run_id)
    .bind(pricing.snapshot.version_id)
    .bind(&pricing.snapshot.checksum)
    .execute(&mut *tx)
    .await
    .map_err(|error| format!("insert Mail accepted usage inbox: {error}"))?;
    let existing: (Vec<u8>, String) = sqlx::query_as(
        "SELECT payload_sha256,status FROM billing.mail_accepted_usage_inbox
         WHERE evidence_id=$1 FOR UPDATE",
    )
    .bind(evidence_id)
    .fetch_one(&mut *tx)
    .await
    .map_err(|error| format!("lock Mail accepted usage inbox: {error}"))?;
    if existing.0 != evidence.evidence_sha256 {
        let _ = tx.rollback().await;
        let _ = pricing_runtime
            .mark_billing_run_retrying(pricing.billing_run_id, fencing_token)
            .await;
        return Err(EVIDENCE_CHECKSUM_CONFLICT.to_string());
    }
    if existing.1 == "SETTLED" {
        tx.commit()
            .await
            .map_err(|error| format!("commit duplicate Mail evidence: {error}"))?;
        pricing_runtime
            .complete_billing_run(pricing.billing_run_id, accepted_at, fencing_token)
            .await
            .map_err(|error| error.to_string())?;
        return Ok(());
    }
    sqlx::query(
        "UPDATE billing.mail_accepted_usage_inbox
         SET status='PROCESSING',retry_count=retry_count+1,reason=NULL WHERE evidence_id=$1",
    )
    .bind(evidence_id)
    .execute(&mut *tx)
    .await
    .map_err(|error| format!("prepare Mail accepted usage replay: {error}"))?;

    let owners = sqlx::query_as::<Postgres, (Uuid, String)>(
        "SELECT owner_id,owner_type::text FROM billing.resource_ownership_projection
         WHERE resource_type='MAIL_CONSUMER' AND resource_id=$1 AND zone_id=$2
           AND effective_from <= $3 AND (effective_to IS NULL OR $3 < effective_to)
         ORDER BY ownership_version DESC LIMIT 2",
    )
    .bind(resource_id)
    .bind(zone_id)
    .bind(accepted_at)
    .fetch_all(&mut *tx)
    .await
    .map_err(|error| format!("resolve Mail consumer owner: {error}"))?;

    if owners.len() != 1 {
        let reason = if owners.is_empty() {
            "OWNER_PROJECTION_MISSING"
        } else {
            "OWNER_PROJECTION_AMBIGUOUS"
        };
        persist_unrated(
            &mut tx,
            MailUnratedLine {
                line_id: ledger_id,
                evidence_id,
                resource_id,
                accepted_at,
                evidence_hash: &evidence_hash,
                pricing_version_id: pricing.snapshot.version_id,
                reason,
            },
        )
        .await?;
        tx.commit()
            .await
            .map_err(|error| format!("commit unrated Mail evidence: {error}"))?;
        let _ = pricing_runtime
            .mark_billing_run_retrying(pricing.billing_run_id, fencing_token)
            .await;
        return Err("Mail accepted usage retains unrated ownership".to_string());
    }

    let (owner_id, owner_type) = &owners[0];
    let amount = pricing
        .snapshot
        .charge_micro_units(1, pricing.adjustment.as_ref())
        .map_err(|error| error.to_string())?;
    if amount > 0 {
        let reference = format!("mail-accepted:{evidence_id}");
        let description = format!("Mail accepted recipient evidence {evidence_id}");
        if let UsageChargeOutcome::Unrated(reason) = settle_usage_charge(
            &mut tx,
            UsageChargeCommand {
                ledger_entry_id: ledger_id,
                owner_id: *owner_id,
                owner_type,
                amount_micro_units: amount,
                module_code: "mail",
                charge_kind_code: CHARGE_KIND,
                reference_id: &reference,
                description: &description,
                usage_settlement_run_id: pricing.billing_run_id,
                pricing_schedule_id: pricing.snapshot.pricing_schedule_id,
                pricing_schedule_version_id: pricing.snapshot.version_id,
                pricing_checksum: &pricing.snapshot.checksum,
                resource_id,
                resource_type: "MAIL_CONSUMER",
                usage_quantity: 1,
                usage_unit: "RECIPIENT",
                occurred_at: accepted_at,
                source_evidence_hash: &evidence_hash,
            },
        )
        .await?
        {
            persist_unrated(
                &mut tx,
                MailUnratedLine {
                    line_id: ledger_id,
                    evidence_id,
                    resource_id,
                    accepted_at,
                    evidence_hash: &evidence_hash,
                    pricing_version_id: pricing.snapshot.version_id,
                    reason,
                },
            )
            .await?;
            tx.commit()
                .await
                .map_err(|error| format!("commit unrated Mail wallet evidence: {error}"))?;
            let _ = pricing_runtime
                .mark_billing_run_retrying(pricing.billing_run_id, fencing_token)
                .await;
            return Err("Mail accepted usage retains unrated wallet".to_string());
        }
    }
    sqlx::query(
        "UPDATE billing.mail_accepted_usage_inbox
         SET amount_micro_units=$1,owner_id=$2,owner_type=$3::billing.owner_type,
             status='SETTLED',reason=NULL,settled_at=NOW()
         WHERE evidence_id=$4",
    )
    .bind(amount)
    .bind(owner_id)
    .bind(owner_type)
    .bind(evidence_id)
    .execute(&mut *tx)
    .await
    .map_err(|error| format!("settle Mail accepted usage inbox: {error}"))?;
    sqlx::query(
        "UPDATE billing.unrated_usage SET status='RESOLVED',last_error=NULL,updated_at=NOW()
         WHERE id=$1 AND status <> 'RESOLVED'",
    )
    .bind(ledger_id)
    .execute(&mut *tx)
    .await
    .map_err(|error| format!("resolve replayed Mail unrated evidence: {error}"))?;
    tx.commit()
        .await
        .map_err(|error| format!("commit Mail accepted usage: {error}"))?;
    pricing_runtime
        .complete_billing_run(pricing.billing_run_id, accepted_at, fencing_token)
        .await
        .map_err(|error| error.to_string())?;
    Ok(())
}

async fn persist_unrated(
    tx: &mut sqlx::Transaction<'_, Postgres>,
    line: MailUnratedLine<'_>,
) -> Result<(), String> {
    sqlx::query(
        "INSERT INTO billing.unrated_usage
         (id,module_code,charge_kind_code,resource_type,resource_id,resource_name,metering_hour,
          usage_quantity,usage_unit,reason,source_report_id,source_evidence_hash,pricing_schedule_version_id)
         VALUES ($1,'mail',$2,'MAIL_CONSUMER',$3,$4,$5,1,'RECIPIENT',$6,$7,$8,$9)
         ON CONFLICT (id) DO UPDATE SET status='PENDING',retry_count=billing.unrated_usage.retry_count+1,
           reason=EXCLUDED.reason,updated_at=NOW()",
    )
    .bind(line.line_id)
    .bind(CHARGE_KIND)
    .bind(line.resource_id)
    .bind(line.resource_id.to_string())
    .bind(line.accepted_at)
    .bind(line.reason)
    .bind(line.evidence_id)
    .bind(line.evidence_hash)
    .bind(line.pricing_version_id)
    .execute(&mut **tx)
    .await
    .map_err(|error| format!("persist unrated Mail accepted usage: {error}"))?;
    sqlx::query(
        "UPDATE billing.mail_accepted_usage_inbox
         SET status='UNRATED',reason=$1,settled_at=NOW() WHERE evidence_id=$2",
    )
    .bind(line.reason)
    .bind(line.evidence_id)
    .execute(&mut **tx)
    .await
    .map_err(|error| format!("mark Mail accepted usage unrated: {error}"))?;
    Ok(())
}

async fn resolve_zone_adjustment(
    pg_pool: &PgPool,
    zone_id: Uuid,
    at: DateTime<Utc>,
) -> Result<Option<RateAdjustmentSnapshot>, String> {
    let row = sqlx::query_as::<Postgres, (Uuid, i32, DateTime<Utc>, i64, i64, String)>(
        "SELECT id,version_number,effective_from,multiplier_numerator,multiplier_denominator,checksum
         FROM billing.mail_zone_price_adjustment_versions
         WHERE zone_id=$1 AND status <> 'CANCELLED' AND effective_from <= $2
           AND (effective_to IS NULL OR $2 < effective_to)
         ORDER BY version_number DESC LIMIT 1",
    )
    .bind(zone_id)
    .bind(at)
    .fetch_optional(pg_pool)
    .await
    .map_err(|error| format!("resolve Mail Zone adjustment: {error}"))?;
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
        return Err(format!("Mail Zone adjustment {id} checksum mismatch"));
    }
    Ok(Some(RateAdjustmentSnapshot {
        id,
        version_number,
        checksum,
        numerator,
        denominator,
    }))
}
