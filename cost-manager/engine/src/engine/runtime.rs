use std::collections::HashMap;
use std::sync::Arc;

use arc_swap::ArcSwap;
use chrono::{DateTime, Utc};
use futures_util::StreamExt;
use moka::future::Cache;
use prost::Message;
use sha2::{Digest, Sha256};
use sqlx::{FromRow, PgPool};
use tokio::sync::watch;
use uuid::Uuid;

use crate::engine::pricing_event_proto;
use crate::engine::snapshot::{
    BillingPricingLease, BillingRunCommand, CatalogSnapshot, PricingError, PricingScheduleSnapshot,
    RateAdjustmentSnapshot, ScalarBracket,
};

pub(crate) const PRICING_EVENT_CHANNEL: &str = "billing.pricing.schedule.version.published";

pub struct PricingRuntime {
    pub(crate) db: PgPool,
    pub(crate) version_cache: Cache<Uuid, Arc<PricingScheduleSnapshot>>,
    pub(crate) active: ArcSwap<CatalogSnapshot>,
}

#[derive(FromRow)]
struct PricingRow {
    pricing_schedule_id: Uuid,
    version_id: Uuid,
    version_number: i32,
    code: String,
    charge_kind: String,
    module_code: String,
    pricing_model: String,
    raw_input_unit: String,
    effective_from: DateTime<Utc>,
    effective_to: Option<DateTime<Utc>>,
    checksum: String,
    currency: String,
    range_start_quantity: i64,
    range_end_quantity: Option<i64>,
    price_numerator_micro_units: i64,
    price_denominator_quantity: i64,
}

impl PricingRuntime {
    pub async fn bootstrap(db: PgPool) -> Result<Arc<Self>, PricingError> {
        let catalog = load_catalog(&db).await?;
        if catalog.versions_by_kind.is_empty() {
            return Err(PricingError(
                "pricing catalog is empty; billing must fail closed".into(),
            ));
        }
        let version_cache = Cache::builder().max_capacity(10_000).build();
        for version in catalog.versions_by_kind.values().flatten() {
            version_cache
                .insert(version.version_id, version.clone())
                .await;
        }
        Ok(Arc::new(Self {
            db,
            version_cache,
            active: ArcSwap::from_pointee(catalog),
        }))
    }

    pub async fn refresh_from_db(
        &self,
        expected: Option<(Uuid, String)>,
    ) -> Result<(), PricingError> {
        let catalog = Arc::new(load_catalog(&self.db).await?);
        if let Some((version_id, checksum)) = expected
            && !catalog.contains_version(version_id, &checksum)
        {
            return Err(PricingError(format!(
                "published pricing version {version_id} missing or checksum mismatch"
            )));
        }
        for version in catalog.versions_by_kind.values().flatten() {
            self.version_cache
                .insert(version.version_id, version.clone())
                .await;
        }
        self.active.store(catalog);
        Ok(())
    }

    pub async fn snapshot_by_version(
        &self,
        version_id: Uuid,
    ) -> Option<Arc<PricingScheduleSnapshot>> {
        self.version_cache
            .get(&version_id)
            .await
            .or_else(|| self.active.load_full().find_version(version_id))
    }

    pub async fn begin_billing_run(
        &self,
        command: BillingRunCommand<'_>,
    ) -> Result<BillingPricingLease, PricingError> {
        if command.source_module.trim().is_empty()
            || command.charge_kind_code.trim().is_empty()
            || command.source_report_id.is_nil()
            || command.requested_end <= command.requested_start
            || command.fencing_token <= 0
            || command
                .adjustment
                .as_ref()
                .is_some_and(|value| value.numerator < 0 || value.denominator <= 0)
        {
            return Err(PricingError("billing run command is invalid".into()));
        }
        if let Some((run_id, version_id, status, pinned_fencing_token, adjustment_id, adjustment_version, adjustment_checksum, adjustment_numerator, adjustment_denominator)) = sqlx::query_as::<_, (Uuid, Uuid, String, i64, Option<Uuid>, Option<i32>, Option<String>, Option<i64>, Option<i64>)>(
			"SELECT id, pricing_schedule_version_id, status, fencing_token, rate_adjustment_id, rate_adjustment_version, rate_adjustment_checksum, rate_adjustment_numerator, rate_adjustment_denominator FROM billing.usage_settlement_runs WHERE source_module=$1 AND source_report_id=$2 AND charge_kind_code=$3 FOR UPDATE",
		)
		.bind(command.source_module).bind(command.source_report_id).bind(command.charge_kind_code).fetch_optional(&self.db).await? {
			let snapshot = self.version_cache.get(&version_id).await.or_else(|| self.active.load_full().find_version(version_id)).ok_or_else(|| PricingError(format!("pinned pricing version {version_id} is unavailable")))?;
			let adjustment = match (adjustment_id, adjustment_version, adjustment_checksum, adjustment_numerator, adjustment_denominator) {
				(Some(id), Some(version_number), Some(checksum), Some(numerator), Some(denominator)) => Some(RateAdjustmentSnapshot { id, version_number, checksum, numerator, denominator }),
				(None, None, None, None, None) => None,
				_ => return Err(PricingError(format!("billing run {run_id} has incomplete rate adjustment lineage"))),
			};
			if status != "COMPLETED" {
				if command.fencing_token < pinned_fencing_token {
					return Err(PricingError(format!("billing run {run_id} rejected stale fencing token")));
				}
				let updated = sqlx::query("UPDATE billing.usage_settlement_runs SET status='RETRYING', fencing_token=$1, updated_at=NOW() WHERE id=$2 AND fencing_token <= $1")
					.bind(command.fencing_token).bind(run_id).execute(&self.db).await?;
				if updated.rows_affected() != 1 {
					return Err(PricingError(format!("billing run {run_id} lost its fencing race")));
				}
			}
			return Ok(BillingPricingLease { billing_run_id: run_id, snapshot, adjustment });
		}
        // A new monetary run must resolve against PostgreSQL at its historical
        // boundary. The in-memory catalog and Redis hint are performance
        // paths only; a missed invalidation must not rate a new report with an
        // expired schedule version.
        let durable_catalog = load_catalog(&self.db).await?;
        let snapshot = durable_catalog
            .resolve(command.charge_kind_code, command.requested_end)
            .ok_or_else(|| {
                PricingError(format!(
                    "no pricing schedule for {} at billing boundary {}",
                    command.charge_kind_code, command.requested_end
                ))
            })?;
        let run_id = Uuid::now_v7();
        self.active.store(Arc::new(durable_catalog));
        self.version_cache
            .insert(snapshot.version_id, snapshot.clone())
            .await;
        sqlx::query("INSERT INTO billing.usage_settlement_runs (id, source_module, source_report_id, charge_kind_code, window_start, window_end, pricing_schedule_id, pricing_schedule_version_id, pricing_checksum, rate_adjustment_id, rate_adjustment_version, rate_adjustment_checksum, rate_adjustment_numerator, rate_adjustment_denominator, fencing_token, status) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,'RUNNING')")
			.bind(run_id).bind(command.source_module).bind(command.source_report_id).bind(command.charge_kind_code).bind(command.requested_start).bind(command.requested_end).bind(snapshot.pricing_schedule_id).bind(snapshot.version_id).bind(&snapshot.checksum)
			.bind(command.adjustment.as_ref().map(|value| value.id)).bind(command.adjustment.as_ref().map(|value| value.version_number)).bind(command.adjustment.as_ref().map(|value| value.checksum.as_str())).bind(command.adjustment.as_ref().map(|value| value.numerator)).bind(command.adjustment.as_ref().map(|value| value.denominator)).bind(command.fencing_token).execute(&self.db).await?;
        Ok(BillingPricingLease {
            billing_run_id: run_id,
            snapshot,
            adjustment: command.adjustment,
        })
    }

    pub async fn complete_billing_run(
        &self,
        run_id: Uuid,
        checkpoint: DateTime<Utc>,
        fencing_token: i64,
    ) -> Result<(), PricingError> {
        let result = sqlx::query("UPDATE billing.usage_settlement_runs SET status='COMPLETED', checkpoint=$1, completed_at=NOW(), updated_at=NOW() WHERE id=$2 AND fencing_token=$3 AND status IN ('RUNNING','RETRYING')").bind(checkpoint).bind(run_id).bind(fencing_token).execute(&self.db).await?;
        if result.rows_affected() != 1 {
            let status: Option<String> =
                sqlx::query_scalar("SELECT status FROM billing.usage_settlement_runs WHERE id=$1")
                    .bind(run_id)
                    .fetch_optional(&self.db)
                    .await?;
            if status.as_deref() == Some("COMPLETED") {
                return Ok(());
            }
            return Err(PricingError(format!(
                "settlement run {run_id} cannot be completed"
            )));
        }
        Ok(())
    }

    pub async fn mark_billing_run_retrying(
        &self,
        run_id: Uuid,
        fencing_token: i64,
    ) -> Result<(), PricingError> {
        let result = sqlx::query("UPDATE billing.usage_settlement_runs SET status='RETRYING', retry_count=retry_count+1, updated_at=NOW() WHERE id=$1 AND fencing_token=$2 AND status IN ('RUNNING','RETRYING')").bind(run_id).bind(fencing_token).execute(&self.db).await?;
        if result.rows_affected() != 1 {
            let status: Option<String> =
                sqlx::query_scalar("SELECT status FROM billing.usage_settlement_runs WHERE id=$1")
                    .bind(run_id)
                    .fetch_optional(&self.db)
                    .await?;
            if status.as_deref() != Some("COMPLETED") {
                return Err(PricingError(format!(
                    "settlement run {run_id} is missing or invalid"
                )));
            }
        }
        Ok(())
    }
}

pub(crate) async fn load_catalog(db: &PgPool) -> Result<CatalogSnapshot, PricingError> {
    let rows = sqlx::query_as::<_, PricingRow>(
		"SELECT s.id AS pricing_schedule_id, v.id AS version_id, v.version_number, s.code, s.charge_kind_code AS charge_kind, c.module_code, s.pricing_model::text, c.raw_input_unit, v.effective_from, v.effective_to, v.checksum, s.currency, b.range_start_quantity, b.range_end_quantity, b.price_numerator_micro_units, b.price_denominator_quantity FROM billing.pricing_schedules s JOIN billing.charge_kind_catalog c ON c.code=s.charge_kind_code JOIN billing.pricing_schedule_versions v ON v.pricing_schedule_id=s.id AND v.status <> 'CANCELLED' JOIN billing.pricing_schedule_scalar_brackets b ON b.pricing_schedule_version_id=v.id WHERE s.status='ACTIVE' AND c.status='ENABLED' ORDER BY s.charge_kind_code, v.version_number, b.range_start_quantity",
	).fetch_all(db).await?;
    let mut grouped: HashMap<Uuid, (PricingRow, Vec<ScalarBracket>)> = HashMap::new();
    for row in rows {
        let bracket = ScalarBracket {
            range_start_quantity: row.range_start_quantity,
            range_end_quantity: row.range_end_quantity,
            price_numerator_micro_units: row.price_numerator_micro_units,
            price_denominator_quantity: row.price_denominator_quantity,
        };
        grouped
            .entry(row.version_id)
            .and_modify(|(_, brackets)| brackets.push(bracket.clone()))
            .or_insert((row, vec![bracket]));
    }
    let mut catalog = CatalogSnapshot::default();
    for (_, (row, brackets)) in grouped {
        validate_brackets(&brackets)?;
        if row.currency != "USD" {
            return Err(PricingError(format!(
                "unsupported pricing currency {} for version {}",
                row.currency, row.version_id
            )));
        }
        let computed = checksum(
            &row.code,
            &row.charge_kind,
            &row.pricing_model,
            &row.currency,
            row.version_number,
            row.effective_from,
            &brackets,
        );
        if row.checksum != computed {
            return Err(PricingError(format!(
                "checksum mismatch for pricing version {}",
                row.version_id
            )));
        }
        let snapshot = Arc::new(PricingScheduleSnapshot {
            pricing_schedule_id: row.pricing_schedule_id,
            version_id: row.version_id,
            module_code: row.module_code,
            charge_kind_code: row.charge_kind.clone(),
            pricing_model: row.pricing_model,
            raw_input_unit: row.raw_input_unit,
            version_number: row.version_number,
            effective_from: row.effective_from,
            effective_to: row.effective_to,
            checksum: row.checksum,
            brackets,
        });
        catalog
            .versions_by_kind
            .entry(row.charge_kind)
            .or_default()
            .push(snapshot);
    }
    for versions in catalog.versions_by_kind.values_mut() {
        versions.sort_by_key(|version| version.version_number);
    }
    Ok(catalog)
}

pub(crate) fn validate_brackets(brackets: &[ScalarBracket]) -> Result<(), PricingError> {
    if brackets.is_empty() || brackets[0].range_start_quantity != 0 {
        return Err(PricingError("pricing brackets must start at zero".into()));
    }
    for (index, current) in brackets.iter().enumerate() {
        if current.range_start_quantity < 0
            || current.price_numerator_micro_units < 0
            || current.price_denominator_quantity <= 0
            || current
                .range_end_quantity
                .is_some_and(|end| end <= current.range_start_quantity)
        {
            return Err(PricingError(
                "pricing bracket contains invalid values".into(),
            ));
        }
        if index + 1 == brackets.len() {
            if current.range_end_quantity.is_some() {
                return Err(PricingError("last pricing bracket must be infinite".into()));
            }
        } else if current.range_end_quantity != Some(brackets[index + 1].range_start_quantity) {
            return Err(PricingError(
                "pricing brackets contain a gap or overlap".into(),
            ));
        }
    }
    Ok(())
}

pub(crate) fn checksum(
    code: &str,
    charge_kind: &str,
    pricing_model: &str,
    currency: &str,
    version: i32,
    effective_from: DateTime<Utc>,
    brackets: &[ScalarBracket],
) -> String {
    let mut hasher = Sha256::new();
    let mut write = |value: &str| {
        hasher.update((value.len() as u64).to_be_bytes());
        hasher.update(value.as_bytes());
    };
    write(code);
    write(charge_kind);
    write(pricing_model);
    write(currency);
    // Billing PostgreSQL persists timestamptz at microsecond precision. The Go
    // publisher uses the same fixed-width UTC representation before hashing.
    write(&effective_from.to_rfc3339_opts(chrono::SecondsFormat::Micros, true));
    write(&version.to_string());
    for bracket in brackets {
        write(&bracket.range_start_quantity.to_string());
        write(
            &bracket
                .range_end_quantity
                .map(|value| value.to_string())
                .unwrap_or_else(|| "infinity".into()),
        );
        write(&bracket.price_numerator_micro_units.to_string());
        write(&bracket.price_denominator_quantity.to_string());
    }
    format!("{:x}", hasher.finalize())
}

pub async fn run_pricing_listener(
    redis_url: String,
    runtime: Arc<PricingRuntime>,
    mut shutdown_rx: watch::Receiver<bool>,
) {
    let mut reconcile = tokio::time::interval(std::time::Duration::from_secs(30));
    loop {
        let client = match redis::Client::open(redis_url.as_str()) {
            Ok(client) => client,
            Err(error) => {
                eprintln!("Pricing listener cannot parse Shared Redis URL: {error}");
                return;
            }
        };
        let mut subscriber = match client.get_async_pubsub().await {
            Ok(connection) => connection,
            Err(error) => {
                eprintln!("Pricing listener cannot connect to Shared Redis: {error}");
                tokio::time::sleep(std::time::Duration::from_secs(1)).await;
                continue;
            }
        };
        if let Err(error) = subscriber.subscribe(PRICING_EVENT_CHANNEL).await {
            eprintln!("Pricing listener cannot subscribe to Shared Redis: {error}");
            tokio::time::sleep(std::time::Duration::from_secs(1)).await;
            continue;
        }
        let mut messages = subscriber.on_message();
        let mut disconnected = false;
        while !disconnected {
            tokio::select! {
                message = messages.next() => {
                    let Some(message) = message else { disconnected = true; continue; };
                    match pricing_event_proto::PricingScheduleVersionPublished::decode(message.get_payload_bytes()) {
                        Ok(event) => match Uuid::parse_str(&event.pricing_schedule_version_id) { Ok(version_id) => if let Err(error) = runtime.refresh_from_db(Some((version_id, event.checksum))).await { eprintln!("Pricing event preload failed: {error}"); }, Err(error) => eprintln!("Pricing event has invalid version id: {error}") },
                        Err(error) => eprintln!("Pricing event protobuf decode failed: {error}"),
                    }
                }
                _ = reconcile.tick() => if let Err(error) = runtime.refresh_from_db(None).await { eprintln!("Pricing periodic reconciliation failed: {error}"); },
                changed = shutdown_rx.changed() => if changed.is_err() || *shutdown_rx.borrow() { return; },
            }
        }
        if *shutdown_rx.borrow() {
            return;
        }
        eprintln!("Pricing listener Shared Redis PubSub disconnected; reconnecting");
        tokio::time::sleep(std::time::Duration::from_secs(1)).await;
    }
}
