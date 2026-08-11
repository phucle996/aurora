use std::collections::HashMap;
use std::sync::Arc;

use arc_swap::ArcSwap;
use chrono::{DateTime, Utc};
use futures_util::StreamExt;
use moka::future::Cache;
use prost::Message;
use sha2::{Digest, Sha256};
use sqlx::{FromRow, PgPool};
use tokio::sync::{Mutex, watch};
use uuid::Uuid;

use crate::engine::pricing_event_proto;
use crate::engine::snapshot::{
    BillingPricingLease, CatalogSnapshot, PricingError, ServiceType, TierPricingSnapshot, TierRange,
};

pub(crate) const PRICING_EVENT_CHANNEL: &str = "billing.pricing.tier_version.published";

pub(crate) struct ActivationState {
    pub(crate) billing_in_progress: usize,
    pub(crate) activation_blocked: bool,
    pub(crate) pending: Option<Arc<CatalogSnapshot>>,
}

pub struct PricingRuntime {
    pub(crate) db: PgPool,
    pub(crate) version_cache: Cache<Uuid, Arc<TierPricingSnapshot>>,
    pub(crate) active: ArcSwap<CatalogSnapshot>,
    pub(crate) state: Mutex<ActivationState>,
}

#[derive(FromRow)]
struct PricingRow {
    tier_version_id: Uuid,
    version_number: i32,
    code: String,
    service_type: String,
    effective_from: DateTime<Utc>,
    effective_to: Option<DateTime<Utc>>,
    checksum: String,
    range_start: i64,
    range_end: i64,
    base_unit_price: i64,
}

impl PricingRuntime {
    // [COMMENT]: Bootstrap từ durable SoT trước khi billing worker được spawn; thiếu/sai catalog là startup failure.
    pub async fn bootstrap(db: PgPool) -> Result<Arc<Self>, PricingError> {
        let catalog = load_catalog(&db).await?;
        if catalog.versions_by_service.is_empty() {
            return Err(PricingError(
                "pricing catalog is empty; billing must fail closed".into(),
            ));
        }
        let version_cache = Cache::builder().max_capacity(10_000).build();
        for version in catalog.versions_by_service.values().flatten() {
            version_cache
                .insert(version.tier_version_id, version.clone())
                .await;
        }
        let incomplete_run_exists: bool = sqlx::query_scalar(
            "SELECT EXISTS (SELECT 1 FROM billing.billing_runs WHERE status IN ('RUNNING','RETRYING'))"
        ).fetch_one(&db).await?;

        Ok(Arc::new(Self {
            db,
            version_cache,
            active: ArcSwap::from_pointee(catalog),
            state: Mutex::new(ActivationState {
                billing_in_progress: 0,
                activation_blocked: incomplete_run_exists,
                pending: None,
            }),
        }))
    }

    // [COMMENT]: Event chỉ kích hoạt reload full catalog; Engine không ghép row-level CDC thành snapshot.
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
        for version in catalog.versions_by_service.values().flatten() {
            self.version_cache
                .insert(version.tier_version_id, version.clone())
                .await;
        }

        let mut state = self.state.lock().await;
        if state.billing_in_progress > 0 || state.activation_blocked {
            state.pending = Some(catalog);
        } else {
            self.active.store(catalog);
        }
        Ok(())
    }

    // [COMMENT]: Begin pin durable version; run dở được resume bằng chính tier_version_id cũ.
    pub async fn begin_billing_run(
        &self,
        service_type: &str,
        requested_start: DateTime<Utc>,
        requested_end: DateTime<Utc>,
        fencing_token: i64,
    ) -> Result<BillingPricingLease, PricingError> {
        let parsed_service = ServiceType::parse(service_type)?;
        let mut state = self.state.lock().await;
        if let Some((run_id, version_id)) = sqlx::query_as::<_, (Uuid, Uuid)>(
            "SELECT id, tier_version_id FROM billing.billing_runs \
             WHERE service_type = $1::billing.service_type AND status IN ('RUNNING','RETRYING') ORDER BY started_at LIMIT 1"
        ).bind(parsed_service.as_str()).fetch_optional(&self.db).await? {
            let snapshot = self.version_cache.get(&version_id).await
                .or_else(|| self.active.load_full().find_version(version_id))
                .ok_or_else(|| PricingError(format!("pinned pricing version {version_id} is unavailable")))?;
            sqlx::query("UPDATE billing.billing_runs SET status='RETRYING', fencing_token=$1, updated_at=NOW() WHERE id=$2")
                .bind(fencing_token).bind(run_id).execute(&self.db).await?;
            state.billing_in_progress = state.billing_in_progress.saturating_add(1);
            state.activation_blocked = true;
            return Ok(BillingPricingLease { billing_run_id: run_id, snapshot });
        }

        // Apply a catalog published while no local run was active. Once the
        // first service run starts, all subsequent service runs in the same
        // report share the same immutable active catalog.
        if state.billing_in_progress == 0
            && !state.activation_blocked
            && let Some(pending) = state.pending.take()
        {
            self.active.store(pending);
        }
        let snapshot = self
            .active
            .load_full()
            .resolve(parsed_service, requested_end)
            .ok_or_else(|| {
                PricingError(format!(
                    "no pricing version for {service_type} at billing boundary {requested_end}"
                ))
            })?;
        let run_id = Uuid::now_v7();
        sqlx::query(
            "INSERT INTO billing.billing_runs \
             (id, service_type, tier_version_id, window_start, window_end, status, fencing_token) \
             VALUES ($1,$2::billing.service_type,$3,$4,$5,'RUNNING',$6)",
        )
        .bind(run_id)
        .bind(parsed_service.as_str())
        .bind(snapshot.tier_version_id)
        .bind(requested_start)
        .bind(requested_end)
        .bind(fencing_token)
        .execute(&self.db)
        .await?;
        state.billing_in_progress = state.billing_in_progress.saturating_add(1);
        state.activation_blocked = true;
        Ok(BillingPricingLease {
            billing_run_id: run_id,
            snapshot,
        })
    }

    // [COMMENT]: Chỉ durable completion mới mở gate và COW pending catalog thành active.
    pub async fn complete_billing_run(
        &self,
        run_id: Uuid,
        checkpoint: DateTime<Utc>,
    ) -> Result<(), PricingError> {
        let result = sqlx::query(
            "UPDATE billing.billing_runs SET status='COMPLETED', checkpoint=$1, completed_at=NOW(), updated_at=NOW() \
             WHERE id=$2 AND status IN ('RUNNING','RETRYING')"
        ).bind(checkpoint).bind(run_id).execute(&self.db).await?;
        if result.rows_affected() != 1 {
            return Err(PricingError(format!(
                "billing run {run_id} cannot be completed"
            )));
        }

        let mut state = self.state.lock().await;
        state.billing_in_progress = state.billing_in_progress.saturating_sub(1);
        if state.billing_in_progress == 0 {
            state.activation_blocked = false;
            if let Some(pending) = state.pending.take() {
                self.active.store(pending);
            }
        }
        Ok(())
    }

    // [COMMENT]: Failed run giữ activation blocked để retry tiếp tục dùng pinned version cũ.
    pub async fn mark_billing_run_retrying(&self, run_id: Uuid) -> Result<(), PricingError> {
        let result = sqlx::query(
            "UPDATE billing.billing_runs SET status='RETRYING', updated_at=NOW() \
             WHERE id=$1 AND status IN ('RUNNING','RETRYING')",
        )
        .bind(run_id)
        .execute(&self.db)
        .await?;
        let mut state = self.state.lock().await;
        state.billing_in_progress = state.billing_in_progress.saturating_sub(1);
        if result.rows_affected() == 1 {
            state.activation_blocked = true;
        } else {
            let status: Option<String> =
                sqlx::query_scalar("SELECT status FROM billing.billing_runs WHERE id=$1")
                    .bind(run_id)
                    .fetch_optional(&self.db)
                    .await?;
            if status.as_deref() == Some("COMPLETED") {
                if state.billing_in_progress == 0 {
                    state.activation_blocked = false;
                    if let Some(pending) = state.pending.take() {
                        self.active.store(pending);
                    }
                }
            } else {
                return Err(PricingError(format!(
                    "billing run {run_id} is missing or invalid"
                )));
            }
        }
        Ok(())
    }
}

pub(crate) async fn load_catalog(db: &PgPool) -> Result<CatalogSnapshot, PricingError> {
    let rows = sqlx::query_as::<_, PricingRow>(
        "SELECT v.id AS tier_version_id, v.version_number, t.code, t.service_type::text, \
                v.effective_from, v.effective_to, v.checksum, \
                r.range_start, r.range_end, r.base_unit_price \
         FROM billing.tiers t \
         JOIN billing.tier_versions v ON v.tier_id=t.id AND v.status <> 'CANCELLED' \
         JOIN billing.tier_version_ranges r ON r.tier_version_id=v.id \
         ORDER BY t.service_type, v.version_number, r.range_start",
    )
    .fetch_all(db)
    .await?;

    let mut grouped: HashMap<Uuid, (PricingRow, Vec<TierRange>)> = HashMap::new();
    for row in rows {
        let tier_range = TierRange {
            range_start: row.range_start,
            range_end: row.range_end,
            base_unit_price: row.base_unit_price,
        };
        grouped
            .entry(row.tier_version_id)
            .and_modify(|(_, ranges)| ranges.push(tier_range.clone()))
            .or_insert((row, vec![tier_range]));
    }

    let mut catalog = CatalogSnapshot::default();
    for (_, (row, ranges)) in grouped {
        validate_ranges(&ranges)?;
        let parsed_service = ServiceType::parse(&row.service_type)?;
        let computed = checksum(&row.code, parsed_service.as_str(), &ranges);
        // [COMMENT]: Legacy backfill có md5 32 ký tự; version mới bắt buộc SHA-256 khớp tuyệt đối.
        if row.checksum.len() == 64 && row.checksum != computed {
            return Err(PricingError(format!(
                "checksum mismatch for pricing version {}",
                row.tier_version_id
            )));
        }
        let snapshot = Arc::new(TierPricingSnapshot {
            tier_version_id: row.tier_version_id,
            version_number: row.version_number,
            effective_from: row.effective_from,
            effective_to: row.effective_to,
            checksum: row.checksum,
            ranges,
        });
        catalog
            .versions_by_service
            .entry(parsed_service)
            .or_default()
            .push(snapshot);
    }
    for versions in catalog.versions_by_service.values_mut() {
        versions.sort_by_key(|version| version.version_number);
    }
    Ok(catalog)
}

pub(crate) fn validate_ranges(ranges: &[TierRange]) -> Result<(), PricingError> {
    if ranges.is_empty() || ranges[0].range_start != 0 {
        return Err(PricingError("pricing ranges must start at zero".into()));
    }
    for (index, current) in ranges.iter().enumerate() {
        if current.range_start < 0
            || current.base_unit_price < 0
            || (current.range_end != 0 && current.range_end <= current.range_start)
        {
            return Err(PricingError(
                "pricing range contains negative or reversed values".into(),
            ));
        }
        if index + 1 == ranges.len() {
            if current.range_end != 0 {
                return Err(PricingError("last pricing range must be infinite".into()));
            }
        } else if current.range_end == 0 || current.range_end != ranges[index + 1].range_start {
            return Err(PricingError(
                "pricing ranges contain a gap or overlap".into(),
            ));
        }
    }
    Ok(())
}

pub(crate) fn checksum(code: &str, service_type: &str, ranges: &[TierRange]) -> String {
    let mut hasher = Sha256::new();
    hasher.update(code.as_bytes());
    hasher.update([0]);
    hasher.update(service_type.as_bytes());
    hasher.update([0]);
    for tier_range in ranges {
        hasher.update(format!(
            "{}:{}:{};",
            tier_range.range_start, tier_range.range_end, tier_range.base_unit_price
        ));
    }
    format!("{:x}", hasher.finalize())
}

// run_pricing_listener preload event version; periodic reconciliation tự chữa event bị mất khi
// Shared Redis PubSub gián đoạn. PubSub chỉ là latency hint, không phải pricing SoT.
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
                    let Some(message) = message else {
                        disconnected = true;
                        continue;
                    };
                    match pricing_event_proto::TierVersionPublished::decode(message.get_payload_bytes()) {
                        Ok(event) => {
                            match ServiceType::parse(&event.service_type) {
                                Ok(_) => {
                                    match Uuid::parse_str(&event.tier_version_id) {
                                        Ok(version_id) => {
                                            if let Err(error) = runtime.refresh_from_db(Some((version_id, event.checksum))).await {
                                                eprintln!("Pricing event preload failed: {error}");
                                            }
                                        }
                                        Err(error) => eprintln!("Pricing event has invalid version id: {error}"),
                                    }
                                }
                                Err(error) => {
                                    eprintln!("Pricing event has unknown service type: {error}");
                                }
                            }
                        }
                        Err(error) => eprintln!("Pricing event protobuf decode failed: {error}"),
                    }
                }
                _ = reconcile.tick() => {
                    if let Err(error) = runtime.refresh_from_db(None).await {
                        eprintln!("Pricing periodic reconciliation failed: {error}");
                    }
                }
                changed = shutdown_rx.changed() => {
                    if changed.is_err() || *shutdown_rx.borrow() {
                        return;
                    }
                }
            }
        }
        if *shutdown_rx.borrow() {
            return;
        }
        eprintln!("Pricing listener Shared Redis PubSub disconnected; reconnecting");
        tokio::time::sleep(std::time::Duration::from_secs(1)).await;
    }
}
