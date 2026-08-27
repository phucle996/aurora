use std::sync::Arc;
use std::time::Duration;

use chrono::{DateTime, Utc};
use sqlx::{FromRow, PgPool, Postgres, Transaction};
use tokio::sync::watch;
use uuid::Uuid;

use crate::engine::{PricingRuntime, RateAdjustmentSnapshot};
use crate::service::storage::zone_adjustment_checksum;

#[derive(Debug, FromRow)]
struct PendingActivation {
    wallet_id: Uuid,
    owner_id: Uuid,
    owner_type: String,
}

#[derive(Debug, FromRow)]
struct PendingLine {
    line_id: Uuid,
    report_id: Uuid,
    zone_id: Uuid,
    resource_id: Uuid,
    resource_name: Option<String>,
    charge_kind_code: String,
    usage_quantity: i64,
    usage_unit: String,
    metering_hour: DateTime<Utc>,
    usage_settlement_run_id: Option<Uuid>,
    pinned_run_id: Option<Uuid>,
    run_pricing_schedule_version_id: Option<Uuid>,
    run_pricing_checksum: Option<String>,
    pricing_schedule_version_id: Option<Uuid>,
    pricing_checksum: Option<String>,
    rate_adjustment_id: Option<Uuid>,
    rate_adjustment_version: Option<i32>,
    rate_adjustment_checksum: Option<String>,
    rate_adjustment_numerator: Option<i64>,
    rate_adjustment_denominator: Option<i64>,
    source_evidence_hash: Option<String>,
}

/// Re-rates only Storage lines that were durably marked pending activation.
/// The worker is intentionally Storage-owned: it does not become a generic
/// unrated collector and never contacts a Zone or a request path.
pub async fn run_pending_activation_reconciliation(
    pg_pool: PgPool,
    pricing_runtime: Arc<PricingRuntime>,
    mut shutdown_rx: watch::Receiver<bool>,
) {
    let mut ticker = tokio::time::interval(Duration::from_secs(30));
    loop {
        tokio::select! {
            _ = ticker.tick() => {
                if let Err(error) = reconcile_batch(&pg_pool, &pricing_runtime).await {
                    eprintln!("Storage pending activation reconciliation failed: {error}");
                }
            }
            changed = shutdown_rx.changed() => {
                if changed.is_err() || *shutdown_rx.borrow() { return; }
            }
        }
    }
}

async fn reconcile_batch(
    pg_pool: &PgPool,
    pricing_runtime: &Arc<PricingRuntime>,
) -> Result<(), String> {
    let mut tx = pg_pool
        .begin()
        .await
        .map_err(|error| format!("begin pending activation reconciliation: {error}"))?;
    let requests = sqlx::query_as::<Postgres, PendingActivation>(
        "SELECT wallet_id, owner_id, owner_type::text
         FROM billing.storage_pending_activation_reconcile
         WHERE status IN ('PENDING','BLOCKED')
         ORDER BY updated_at, wallet_id
         FOR UPDATE SKIP LOCKED
         LIMIT 20",
    )
    .fetch_all(&mut *tx)
    .await
    .map_err(|error| format!("load pending activation requests: {error}"))?;

    for request in requests {
        reconcile_request(&mut tx, pricing_runtime, request).await?;
    }
    tx.commit()
        .await
        .map_err(|error| format!("commit pending activation reconciliation: {error}"))
}

async fn reconcile_request(
    tx: &mut Transaction<'_, Postgres>,
    pricing_runtime: &Arc<PricingRuntime>,
    request: PendingActivation,
) -> Result<(), String> {
    sqlx::query(
        "UPDATE billing.storage_pending_activation_reconcile
         SET status='PROCESSING', retry_count=retry_count+1, updated_at=NOW()
         WHERE wallet_id=$1",
    )
    .bind(request.wallet_id)
    .execute(&mut **tx)
    .await
    .map_err(|error| format!("mark pending activation processing: {error}"))?;

    let wallet = sqlx::query_as::<Postgres, (String, Option<String>, i64, i64, i64, i64)>(
        "SELECT status::text, restriction_reason, cash_balance, promotional_balance, overdraft_limit, version
         FROM billing.wallets WHERE id=$1 AND owner_id=$2 AND owner_type=$3::billing.owner_type FOR UPDATE",
    )
    .bind(request.wallet_id)
    .bind(request.owner_id)
    .bind(&request.owner_type)
    .fetch_optional(&mut **tx)
    .await
    .map_err(|error| format!("lock pending activation wallet: {error}"))?;
    let Some((
        wallet_status,
        restriction_reason,
        mut cash_balance,
        mut promotional_balance,
        overdraft_limit,
        _,
    )) = wallet
    else {
        return mark_request(tx, request.wallet_id, "BLOCKED", Some("WALLET_MISSING")).await;
    };
    if wallet_status != "PENDING_ACTIVATION"
        && !(wallet_status == "SUSPENDED"
            && restriction_reason.as_deref() == Some("CREDIT_EXHAUSTED"))
    {
        return mark_request(
            tx,
            request.wallet_id,
            "BLOCKED",
            Some("WALLET_STATE_CHANGED_BEFORE_RECONCILE"),
        )
        .await;
    }

    let lines = sqlx::query_as::<Postgres, PendingLine>(
        "SELECT u.id AS line_id, l.report_id, l.zone_id, l.resource_id,
                l.resource_name, l.charge_kind_code, l.usage_quantity, l.usage_unit,
                u.metering_hour, l.usage_settlement_run_id, r.id AS pinned_run_id,
                r.pricing_schedule_version_id AS run_pricing_schedule_version_id,
                r.pricing_checksum AS run_pricing_checksum,
                l.pricing_schedule_version_id, l.pricing_checksum,
                r.rate_adjustment_id, r.rate_adjustment_version,
                r.rate_adjustment_checksum, r.rate_adjustment_numerator,
                r.rate_adjustment_denominator,
                u.source_evidence_hash
         FROM billing.unrated_usage u
         JOIN billing.storage_usage_line_inbox l ON l.line_id=u.id
         LEFT JOIN billing.usage_settlement_runs r ON r.id=l.usage_settlement_run_id
         WHERE u.reason='WALLET_PENDING_ACTIVATION'
           AND u.status IN ('PENDING','PROCESSING')
           AND l.owner_id=$1 AND l.owner_type=$2::billing.owner_type
         ORDER BY u.metering_hour, u.id
         FOR UPDATE OF u, l
         LIMIT 500",
    )
    .bind(request.owner_id)
    .bind(&request.owner_type)
    .fetch_all(&mut **tx)
    .await
    .map_err(|error| format!("load pending activation usage: {error}"))?;

    let mut blocked_reason: Option<&'static str> = None;
    let mut current_wallet_status = wallet_status;
    for line in lines {
        let owner = sqlx::query_as::<Postgres, (Uuid, Uuid, String)>(
            "SELECT resource_id, owner_id, owner_type::text
             FROM billing.resource_ownership_projection
             WHERE resource_type='STORAGE_BUCKET'
               AND zone_id=$1
               AND effective_from <= $2
               AND (effective_to IS NULL OR $2 < effective_to)
               AND (resource_id=$3 OR ($4 <> '' AND resource_name=$4))
             ORDER BY ownership_version DESC
             LIMIT 2",
        )
        .bind(line.zone_id)
        .bind(line.metering_hour)
        .bind(line.resource_id)
        .bind(line.resource_name.as_deref().unwrap_or(""))
        .fetch_all(&mut **tx)
        .await
        .map_err(|error| format!("resolve pending activation ownership: {error}"))?;
        if owner.len() != 1 || owner[0].1 != request.owner_id || owner[0].2 != request.owner_type {
            blocked_reason = Some(if owner.is_empty() {
                "OWNER_PROJECTION_MISSING"
            } else {
                "OWNER_PROJECTION_NOT_READY"
            });
            continue;
        }

        let Some(version_id) = line.pricing_schedule_version_id else {
            blocked_reason = Some("PRICING_VERSION_MISSING");
            continue;
        };
        let Some(snapshot) = pricing_runtime.snapshot_by_version(version_id).await else {
            blocked_reason = Some("PRICING_VERSION_UNAVAILABLE");
            continue;
        };
        if line.pricing_checksum.as_deref() != Some(snapshot.checksum.as_str()) {
            blocked_reason = Some("PRICING_CHECKSUM_MISMATCH");
            continue;
        }
        let Some(usage_run_id) = line.usage_settlement_run_id else {
            blocked_reason = Some("PRICING_RUN_MISSING");
            continue;
        };
        if line.pinned_run_id != Some(usage_run_id)
            || line.run_pricing_schedule_version_id != Some(version_id)
            || line.run_pricing_checksum.as_deref() != Some(snapshot.checksum.as_str())
            || snapshot.module_code != "storage"
            || snapshot.charge_kind_code != line.charge_kind_code
            || snapshot.raw_input_unit != line.usage_unit
            || line
                .source_evidence_hash
                .as_deref()
                .is_none_or(str::is_empty)
        {
            blocked_reason = Some("PINNED_PRICING_EVIDENCE_INVALID");
            continue;
        }
        let adjustment = match (
            line.rate_adjustment_id,
            line.rate_adjustment_version,
            line.rate_adjustment_checksum,
            line.rate_adjustment_numerator,
            line.rate_adjustment_denominator,
        ) {
            (
                Some(id),
                Some(version_number),
                Some(checksum),
                Some(numerator),
                Some(denominator),
            ) => {
                let durable =
                    sqlx::query_as::<Postgres, (Uuid, i32, DateTime<Utc>, i64, i64, String)>(
                        "SELECT zone_id, version_number, effective_from, multiplier_numerator,
                            multiplier_denominator, checksum
                     FROM billing.storage_zone_price_adjustment_versions WHERE id=$1",
                    )
                    .bind(id)
                    .fetch_optional(&mut **tx)
                    .await
                    .map_err(|error| format!("verify pending activation adjustment: {error}"))?;
                let Some((
                    durable_zone,
                    durable_version,
                    effective_from,
                    durable_numerator,
                    durable_denominator,
                    durable_checksum,
                )) = durable
                else {
                    blocked_reason = Some("RATE_ADJUSTMENT_UNAVAILABLE");
                    continue;
                };
                if durable_zone != line.zone_id
                    || durable_version != version_number
                    || durable_numerator != numerator
                    || durable_denominator != denominator
                    || durable_checksum != checksum
                    || zone_adjustment_checksum(
                        durable_zone,
                        durable_version,
                        effective_from,
                        durable_numerator,
                        durable_denominator,
                    ) != checksum
                {
                    blocked_reason = Some("RATE_ADJUSTMENT_CHECKSUM_MISMATCH");
                    continue;
                }
                Some(RateAdjustmentSnapshot {
                    id,
                    version_number,
                    checksum,
                    numerator,
                    denominator,
                })
            }
            (None, None, None, None, None) => None,
            _ => {
                blocked_reason = Some("RATE_ADJUSTMENT_LINEAGE_INVALID");
                continue;
            }
        };
        let quantity = u64::try_from(line.usage_quantity).map_err(|_| {
            "pending activation usage quantity is outside unsigned range".to_string()
        })?;
        let cost = snapshot
            .charge_micro_units(quantity, adjustment.as_ref())
            .map_err(|error| error.to_string())?;

        let already_settled: bool = sqlx::query_scalar(
            "SELECT EXISTS (SELECT 1 FROM billing.wallet_ledger_entries WHERE id=$1)",
        )
        .bind(line.line_id)
        .fetch_one(&mut **tx)
        .await
        .map_err(|error| format!("check pending activation ledger: {error}"))?;
        if !already_settled && cost > 0 {
            let promo_debit = promotional_balance.min(cost);
            let new_promotional_balance = promotional_balance - promo_debit;
            let cash_debit = cost - promo_debit;
            cash_balance = cash_balance
                .checked_sub(cash_debit)
                .ok_or_else(|| "pending activation cash balance overflow".to_string())?;
            promotional_balance = new_promotional_balance;
            let status = if current_wallet_status == "PENDING_ACTIVATION"
                && cash_balance
                    .saturating_add(promotional_balance)
                    .saturating_add(overdraft_limit)
                    <= 0
            {
                "SUSPENDED"
            } else {
                current_wallet_status.as_str()
            };
            let reason = if status == "SUSPENDED" {
                "CREDIT_EXHAUSTED"
            } else {
                "NOT_ACTIVATED"
            };
            let wallet_version: i64 = sqlx::query_scalar(
                "UPDATE billing.wallets
                 SET cash_balance=$1, promotional_balance=$2, status=$3::billing.wallet_lifecycle_status,
                     restriction_reason=$4, status_changed_at=CASE WHEN status::text IS DISTINCT FROM $3 THEN NOW() ELSE status_changed_at END,
                     version=version+1, updated_at=NOW()
                 WHERE id=$5 RETURNING version",
            )
            .bind(cash_balance)
            .bind(promotional_balance)
            .bind(status)
            .bind(reason)
            .bind(request.wallet_id)
            .fetch_one(&mut **tx)
            .await
            .map_err(|error| format!("debit pending activation wallet: {error}"))?;
            if status != current_wallet_status {
                sqlx::query(
                    "INSERT INTO billing.wallet_admission_outbox
                     (event_id, wallet_id, owner_id, owner_type, wallet_version, admission_mode, restriction_reason, effective_at)
                     VALUES ($1,$2,$3,$4::billing.owner_type,$5,'SUSPEND_BILLABLE',$6,NOW())",
                )
                .bind(Uuid::now_v7())
                .bind(request.wallet_id)
                .bind(request.owner_id)
                .bind(&request.owner_type)
                .bind(wallet_version)
                .bind(reason)
                .execute(&mut **tx)
                .await
                .map_err(|error| format!("write pending activation suspension: {error}"))?;
                current_wallet_status = status.to_string();
            }
            sqlx::query(
                "INSERT INTO billing.wallet_ledger_entries
                 (id, wallet_id, owner_id, owner_type, amount_micro_units,
                  cash_balance_after, promotional_balance_after, currency, entry_type,
                  module_code, charge_kind_code, reference_id, description, usage_settlement_run_id,
                  pricing_schedule_id, pricing_schedule_version_id, pricing_checksum,
                  resource_id, resource_type, usage_quantity, usage_unit, occurred_at,
                  source_evidence_hash)
                 VALUES ($1,$2,$3,$4::billing.owner_type,$5,$6,$7,'USD','USAGE_CHARGE',
                         'storage',$8,$9,$10,$11,$12,$13,$14,$15,'STORAGE_BUCKET',$16,$17,$18,$19)",
            )
            .bind(line.line_id)
            .bind(request.wallet_id)
            .bind(request.owner_id)
            .bind(&request.owner_type)
            .bind(-cost)
            .bind(cash_balance)
            .bind(promotional_balance)
            .bind(&line.charge_kind_code)
            .bind(format!(
                "storage-report:{}:{}",
                line.report_id, line.resource_id
            ))
            .bind(format!(
                "Storage {} quantity {} after wallet activation",
                line.charge_kind_code, line.usage_quantity
            ))
            .bind(usage_run_id)
            .bind(snapshot.pricing_schedule_id)
            .bind(snapshot.version_id)
            .bind(&snapshot.checksum)
            .bind(owner[0].0)
            .bind(line.usage_quantity)
            .bind(&line.usage_unit)
            .bind(line.metering_hour)
            .bind(line.source_evidence_hash.as_deref())
            .execute(&mut **tx)
            .await
            .map_err(|error| format!("insert pending activation ledger: {error}"))?;
        }

        sqlx::query(
            "UPDATE billing.storage_usage_line_inbox
             SET amount_micro_units=$1, owner_id=$2, owner_type=$3::billing.owner_type,
                 status='SETTLED', settled_at=NOW()
             WHERE line_id=$4",
        )
        .bind(cost)
        .bind(request.owner_id)
        .bind(&request.owner_type)
        .bind(line.line_id)
        .execute(&mut **tx)
        .await
        .map_err(|error| format!("settle pending activation line: {error}"))?;
        sqlx::query(
            "UPDATE billing.unrated_usage SET status='RESOLVED', updated_at=NOW() WHERE id=$1",
        )
        .bind(line.line_id)
        .execute(&mut **tx)
        .await
        .map_err(|error| format!("resolve pending activation unrated line: {error}"))?;
        sqlx::query(
            "UPDATE billing.storage_pending_activation_reconcile
             SET checkpoint_window_end=$1, updated_at=NOW()
             WHERE wallet_id=$2",
        )
        .bind(line.metering_hour)
        .bind(request.wallet_id)
        .execute(&mut **tx)
        .await
        .map_err(|error| format!("checkpoint pending activation reconciliation: {error}"))?;
    }

    let remaining: i64 = sqlx::query_scalar(
        "SELECT COUNT(*)
         FROM billing.unrated_usage u
         JOIN billing.storage_usage_line_inbox l ON l.line_id=u.id
         WHERE u.reason='WALLET_PENDING_ACTIVATION'
           AND u.status IN ('PENDING','PROCESSING')
           AND l.owner_id=$1 AND l.owner_type=$2::billing.owner_type",
    )
    .bind(request.owner_id)
    .bind(&request.owner_type)
    .fetch_one(&mut **tx)
    .await
    .map_err(|error| format!("count pending activation evidence: {error}"))?;
    if blocked_reason.is_some() {
        return mark_request(
            tx,
            request.wallet_id,
            "BLOCKED",
            blocked_reason.or(Some("PENDING_USAGE_REMAINS")),
        )
        .await;
    }
    if remaining > 0 {
        return mark_request(
            tx,
            request.wallet_id,
            "PENDING",
            Some("PENDING_USAGE_REMAINS"),
        )
        .await;
    }

    let (status, cash, promotional, overdraft): (String, i64, i64, i64) = sqlx::query_as(
        "SELECT status::text, cash_balance, promotional_balance, overdraft_limit
         FROM billing.wallets WHERE id=$1 FOR UPDATE",
    )
    .bind(request.wallet_id)
    .fetch_one(&mut **tx)
    .await
    .map_err(|error| format!("reload pending activation wallet: {error}"))?;
    if status == "PENDING_ACTIVATION"
        && cash.saturating_add(promotional).saturating_add(overdraft) > 0
    {
        let wallet_version: i64 = sqlx::query_scalar(
            "UPDATE billing.wallets
             SET status='ACTIVE', restriction_reason=NULL, status_changed_at=NOW(), version=version+1, updated_at=NOW()
             WHERE id=$1 RETURNING version",
        )
        .bind(request.wallet_id)
        .fetch_one(&mut **tx)
        .await
        .map_err(|error| format!("activate reconciled wallet: {error}"))?;
        sqlx::query(
            "INSERT INTO billing.wallet_admission_outbox
             (event_id, wallet_id, owner_id, owner_type, wallet_version, admission_mode, restriction_reason, effective_at)
             VALUES ($1,$2,$3,$4::billing.owner_type,$5,'ALLOW',NULL,NOW())",
        )
        .bind(Uuid::now_v7())
        .bind(request.wallet_id)
        .bind(request.owner_id)
        .bind(&request.owner_type)
        .bind(wallet_version)
        .execute(&mut **tx)
        .await
        .map_err(|error| format!("write reconciled wallet admission: {error}"))?;
    }
    mark_request(tx, request.wallet_id, "COMPLETED", None).await
}

async fn mark_request(
    tx: &mut Transaction<'_, Postgres>,
    wallet_id: Uuid,
    status: &str,
    reason: Option<&str>,
) -> Result<(), String> {
    sqlx::query(
        "UPDATE billing.storage_pending_activation_reconcile
         SET status=$1, last_error=$2, updated_at=NOW()
         WHERE wallet_id=$3",
    )
    .bind(status)
    .bind(reason)
    .bind(wallet_id)
    .execute(&mut **tx)
    .await
    .map_err(|error| format!("update pending activation request: {error}"))?;
    Ok(())
}

#[cfg(test)]
#[path = "../../../tests/unit/storage_pending_activation.rs"]
mod tests;
