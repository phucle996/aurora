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
    BillingPricingLease, CatalogSnapshot, ChargeKind, PricingError, PricingScheduleSnapshot,
    ScalarBracket,
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
    scope_type: String,
    zone_id: Option<Uuid>,
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
        if let Some((version_id, checksum)) = expected {
            if !catalog.contains_version(version_id, &checksum) {
                return Err(PricingError(format!(
                    "published pricing version {version_id} missing or checksum mismatch"
                )));
            }
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
        charge_kind: &str,
        source_report_id: Uuid,
        zone_id: Uuid,
        requested_start: DateTime<Utc>,
        requested_end: DateTime<Utc>,
        fencing_token: i64,
    ) -> Result<BillingPricingLease, PricingError> {
        let kind = ChargeKind::parse(charge_kind)?;
        if let Some((run_id, version_id, status)) = sqlx::query_as::<_, (Uuid, Uuid, String)>(
			"SELECT id, pricing_schedule_version_id, status FROM billing.usage_settlement_runs WHERE source_module='storage' AND source_report_id=$1 AND charge_kind_code=$2 FOR UPDATE",
		)
		.bind(source_report_id).bind(kind.as_str()).fetch_optional(&self.db).await? {
			let snapshot = self.version_cache.get(&version_id).await.or_else(|| self.active.load_full().find_version(version_id)).ok_or_else(|| PricingError(format!("pinned pricing version {version_id} is unavailable")))?;
			if status != "COMPLETED" {
				sqlx::query("UPDATE billing.usage_settlement_runs SET status='RETRYING', fencing_token=$1, updated_at=NOW() WHERE id=$2")
					.bind(fencing_token).bind(run_id).execute(&self.db).await?;
			}
			return Ok(BillingPricingLease { billing_run_id: run_id, snapshot });
		}
        // A new monetary run must resolve against PostgreSQL at its historical
        // boundary. The in-memory catalog and Redis hint are performance
        // paths only; a missed invalidation must not rate a new report with an
        // expired schedule version.
        let durable_catalog = load_catalog(&self.db).await?;
        let snapshot = durable_catalog
            .resolve(kind, zone_id, requested_end)
            .ok_or_else(|| {
                PricingError(format!(
                    "no pricing schedule for {charge_kind} at billing boundary {requested_end}"
                ))
            })?;
        let run_id = Uuid::now_v7();
        self.active.store(Arc::new(durable_catalog));
        self.version_cache
            .insert(snapshot.version_id, snapshot.clone())
            .await;
        sqlx::query("INSERT INTO billing.usage_settlement_runs (id, source_module, source_report_id, charge_kind_code, zone_id, window_start, window_end, pricing_schedule_id, pricing_schedule_version_id, pricing_checksum, fencing_token, status) VALUES ($1,'storage',$2,$3,$4,$5,$6,$7,$8,$9,$10,'RUNNING')")
			.bind(run_id).bind(source_report_id).bind(kind.as_str()).bind(zone_id).bind(requested_start).bind(requested_end).bind(snapshot.pricing_schedule_id).bind(snapshot.version_id).bind(&snapshot.checksum).bind(fencing_token).execute(&self.db).await?;
        Ok(BillingPricingLease {
            billing_run_id: run_id,
            snapshot,
        })
    }

    pub async fn complete_billing_run(
        &self,
        run_id: Uuid,
        checkpoint: DateTime<Utc>,
    ) -> Result<(), PricingError> {
        let result = sqlx::query("UPDATE billing.usage_settlement_runs SET status='COMPLETED', checkpoint=$1, completed_at=NOW(), updated_at=NOW() WHERE id=$2 AND status IN ('RUNNING','RETRYING')").bind(checkpoint).bind(run_id).execute(&self.db).await?;
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

    pub async fn mark_billing_run_retrying(&self, run_id: Uuid) -> Result<(), PricingError> {
        let result = sqlx::query("UPDATE billing.usage_settlement_runs SET status='RETRYING', retry_count=retry_count+1, updated_at=NOW() WHERE id=$1 AND status IN ('RUNNING','RETRYING')").bind(run_id).execute(&self.db).await?;
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
		"SELECT s.id AS pricing_schedule_id, v.id AS version_id, v.version_number, s.code, s.charge_kind_code, c.module_code, s.scope_type::text, s.zone_id, v.effective_from, v.effective_to, v.checksum, s.currency, b.range_start_quantity, b.range_end_quantity, b.price_numerator_micro_units, b.price_denominator_quantity FROM billing.pricing_schedules s JOIN billing.charge_kind_catalog c ON c.code=s.charge_kind_code JOIN billing.pricing_schedule_versions v ON v.pricing_schedule_id=s.id AND v.status <> 'CANCELLED' JOIN billing.pricing_schedule_scalar_brackets b ON b.pricing_schedule_version_id=v.id WHERE s.status='ACTIVE' AND c.status='ENABLED' ORDER BY s.charge_kind_code, v.version_number, b.range_start_quantity",
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
        let kind = ChargeKind::parse(&row.charge_kind)?;
        if row.module_code != "storage" {
            return Err(PricingError(format!(
                "unsupported pricing module {} for version {}",
                row.module_code, row.version_id
            )));
        }
        if row.currency != "USD" {
            return Err(PricingError(format!(
                "unsupported pricing currency {} for version {}",
                row.currency, row.version_id
            )));
        }
        let computed = checksum(
            &row.code,
            kind,
            &row.scope_type,
            row.zone_id,
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
            charge_kind: kind,
            scope_type: row.scope_type,
            zone_id: row.zone_id,
            version_number: row.version_number,
            effective_from: row.effective_from,
            effective_to: row.effective_to,
            checksum: row.checksum,
            brackets,
        });
        catalog
            .versions_by_kind
            .entry(kind)
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
    kind: ChargeKind,
    scope: &str,
    zone_id: Option<Uuid>,
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
    write(kind.as_str());
    write("PROGRESSIVE_UNIT");
    write(scope);
    if let Some(zone_id) = zone_id.filter(|zone_id| *zone_id != Uuid::nil()) {
        write(&zone_id.to_string());
    }
    write(currency);
    write(&effective_from.to_rfc3339_opts(chrono::SecondsFormat::Nanos, true));
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
