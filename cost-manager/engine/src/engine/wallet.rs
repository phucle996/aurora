use chrono::{DateTime, Utc};
use sqlx::{Postgres, Row, Transaction};
use uuid::Uuid;

pub struct UsageChargeCommand<'a> {
    pub ledger_entry_id: Uuid,
    pub owner_id: Uuid,
    pub owner_type: &'a str,
    pub amount_micro_units: i64,
    pub module_code: &'a str,
    pub charge_kind_code: &'a str,
    pub reference_id: &'a str,
    pub description: &'a str,
    pub usage_settlement_run_id: Uuid,
    pub pricing_schedule_id: Uuid,
    pub pricing_schedule_version_id: Uuid,
    pub pricing_checksum: &'a str,
    pub resource_id: Uuid,
    pub resource_type: &'a str,
    pub usage_quantity: i64,
    pub usage_unit: &'a str,
    pub occurred_at: DateTime<Utc>,
    pub source_evidence_hash: &'a str,
}

pub enum UsageChargeOutcome {
    Settled,
    Unrated(&'static str),
}

/// Atomic PAYG wallet/ledger primitive. Module adapters resolve authority,
/// price and evidence before calling it; this function owns only wallet lock,
/// debit ordering, admission transition and deterministic ledger identity.
pub async fn settle_usage_charge(
    tx: &mut Transaction<'_, Postgres>,
    command: UsageChargeCommand<'_>,
) -> Result<UsageChargeOutcome, String> {
    if command.amount_micro_units <= 0 {
        return Ok(UsageChargeOutcome::Settled);
    }

    let wallet = sqlx::query_as::<Postgres, (Uuid, i64, i64, i64, String)>(
        "SELECT id, cash_balance, promotional_balance, overdraft_limit, status::text
         FROM billing.wallets
         WHERE owner_id=$1 AND owner_type=$2::billing.owner_type AND currency='USD'
         FOR UPDATE",
    )
    .bind(command.owner_id)
    .bind(command.owner_type)
    .fetch_optional(&mut **tx)
    .await
    .map_err(|error| format!("lock PAYG owner wallet: {error}"))?;
    let Some((wallet_id, cash_balance, promotional_balance, overdraft_limit, status)) = wallet
    else {
        return Ok(UsageChargeOutcome::Unrated("WALLET_MISSING"));
    };
    if status == "CLOSED" {
        return Ok(UsageChargeOutcome::Unrated("WALLET_CLOSED"));
    }
    if status == "PENDING_ACTIVATION" {
        return Ok(UsageChargeOutcome::Unrated("WALLET_PENDING_ACTIVATION"));
    }

    let ledger_already_exists: bool = sqlx::query_scalar(
        "SELECT EXISTS (SELECT 1 FROM billing.wallet_ledger_entries WHERE id=$1)",
    )
    .bind(command.ledger_entry_id)
    .fetch_one(&mut **tx)
    .await
    .map_err(|error| format!("check PAYG ledger identity: {error}"))?;
    if ledger_already_exists {
        return Ok(UsageChargeOutcome::Settled);
    }

    let promo_debit = promotional_balance.min(command.amount_micro_units);
    let new_promotional_balance = promotional_balance - promo_debit;
    let cash_debit = command.amount_micro_units - promo_debit;
    let new_cash_balance = cash_balance
        .checked_sub(cash_debit)
        .ok_or_else(|| "PAYG wallet cash balance exceeds BIGINT".to_string())?;
    let new_status = if status == "ACTIVE"
        && new_cash_balance
            .saturating_add(new_promotional_balance)
            .saturating_add(overdraft_limit)
            <= 0
    {
        "SUSPENDED"
    } else {
        status.as_str()
    };

    let wallet_version = sqlx::query(
        "UPDATE billing.wallets
         SET cash_balance=$1, promotional_balance=$2,
             status=$3::billing.wallet_lifecycle_status,
             restriction_reason=CASE WHEN $3='SUSPENDED' AND $4='ACTIVE' THEN 'CREDIT_EXHAUSTED' WHEN $3='ACTIVE' THEN NULL ELSE restriction_reason END,
             status_changed_at=CASE WHEN status::text IS DISTINCT FROM $3 THEN NOW() ELSE status_changed_at END,
             version=version+1, updated_at=NOW()
         WHERE id=$5 RETURNING version",
    )
    .bind(new_cash_balance)
    .bind(new_promotional_balance)
    .bind(new_status)
    .bind(&status)
    .bind(wallet_id)
    .fetch_one(&mut **tx)
    .await
    .map_err(|error| format!("update PAYG owner wallet: {error}"))?
    .get::<i64, _>("version");

    if new_status != status {
        sqlx::query(
            "INSERT INTO billing.wallet_admission_outbox
             (event_id, wallet_id, owner_id, owner_type, wallet_version, admission_mode, restriction_reason, effective_at)
             VALUES ($1,$2,$3,$4::billing.owner_type,$5,'SUSPEND_BILLABLE','CREDIT_EXHAUSTED',NOW())",
        )
        .bind(Uuid::now_v7())
        .bind(wallet_id)
        .bind(command.owner_id)
        .bind(command.owner_type)
        .bind(wallet_version)
        .execute(&mut **tx)
        .await
        .map_err(|error| format!("write PAYG wallet admission outbox: {error}"))?;
    }

    let result = sqlx::query(
        "INSERT INTO billing.wallet_ledger_entries
         (id, wallet_id, owner_id, owner_type, amount_micro_units,
          cash_balance_after, promotional_balance_after, currency, entry_type,
          module_code, charge_kind_code, reference_id, description, usage_settlement_run_id,
          pricing_schedule_id, pricing_schedule_version_id, pricing_checksum,
          resource_id, resource_type, usage_quantity, usage_unit, occurred_at, source_evidence_hash)
         VALUES ($1,$2,$3,$4::billing.owner_type,$5,$6,$7,'USD','USAGE_CHARGE',
                 $8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)",
    )
    .bind(command.ledger_entry_id)
    .bind(wallet_id)
    .bind(command.owner_id)
    .bind(command.owner_type)
    .bind(-command.amount_micro_units)
    .bind(new_cash_balance)
    .bind(new_promotional_balance)
    .bind(command.module_code)
    .bind(command.charge_kind_code)
    .bind(command.reference_id)
    .bind(command.description)
    .bind(command.usage_settlement_run_id)
    .bind(command.pricing_schedule_id)
    .bind(command.pricing_schedule_version_id)
    .bind(command.pricing_checksum)
    .bind(command.resource_id)
    .bind(command.resource_type)
    .bind(command.usage_quantity)
    .bind(command.usage_unit)
    .bind(command.occurred_at)
    .bind(command.source_evidence_hash)
    .execute(&mut **tx)
    .await;
    if let Err(error) = result {
        if error
            .as_database_error()
            .and_then(|database| database.code())
            .as_deref()
            == Some("23505")
        {
            return Err("PAYG ledger identity appeared concurrently; retry transaction".into());
        }
        return Err(format!("insert PAYG usage ledger: {error}"));
    }
    Ok(UsageChargeOutcome::Settled)
}

#[cfg(test)]
#[path = "../../tests/unit/wallet.rs"]
mod tests;
