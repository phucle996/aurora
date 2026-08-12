use std::collections::HashMap;
use std::fmt::{Display, Formatter};
use std::sync::Arc;

use chrono::{DateTime, Utc};
use uuid::Uuid;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum ChargeKind {
    StorageNetworkIn,
    StorageNetworkOut,
    StorageCapacity,
}

impl ChargeKind {
    pub fn parse(raw: &str) -> Result<Self, PricingError> {
        match raw.trim() {
            "storage.network_in.byte" => Ok(Self::StorageNetworkIn),
            "storage.network_out.byte" => Ok(Self::StorageNetworkOut),
            "storage.capacity.gb_hour" => Ok(Self::StorageCapacity),
            other => Err(PricingError(format!("unknown charge kind: {other}"))),
        }
    }

    pub fn as_str(self) -> &'static str {
        match self {
            Self::StorageNetworkIn => "storage.network_in.byte",
            Self::StorageNetworkOut => "storage.network_out.byte",
            Self::StorageCapacity => "storage.capacity.gb_hour",
        }
    }
}

#[derive(Debug)]
pub struct PricingError(pub String);

impl Display for PricingError {
    fn fmt(&self, f: &mut Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.0)
    }
}

impl std::error::Error for PricingError {}

impl From<sqlx::Error> for PricingError {
    fn from(value: sqlx::Error) -> Self {
        Self(format!("pricing database error: {value}"))
    }
}

#[derive(Debug, Clone)]
pub struct ScalarBracket {
    pub range_start_quantity: i64,
    pub range_end_quantity: Option<i64>,
    pub price_numerator_micro_units: i64,
    pub price_denominator_quantity: i64,
}

#[derive(Debug, Clone)]
pub struct PricingScheduleSnapshot {
    pub pricing_schedule_id: Uuid,
    pub version_id: Uuid,
    pub charge_kind: ChargeKind,
    pub scope_type: String,
    pub zone_id: Option<Uuid>,
    pub version_number: i32,
    pub effective_from: DateTime<Utc>,
    pub effective_to: Option<DateTime<Utc>>,
    pub checksum: String,
    pub brackets: Vec<ScalarBracket>,
}

impl PricingScheduleSnapshot {
    pub fn charge_micro_units_for_bytes(&self, quantity_bytes: u64) -> Result<i64, PricingError> {
        if self.charge_kind == ChargeKind::StorageCapacity {
            return Err(PricingError(
                "capacity schedule cannot rate byte quantity".into(),
            ));
        }
        charge_progressive(quantity_bytes, &self.brackets)
    }

    pub fn charge_micro_units_for_storage_gb_hours_micros(
        &self,
        quantity_micros: u64,
    ) -> Result<i64, PricingError> {
        if self.charge_kind != ChargeKind::StorageCapacity {
            return Err(PricingError(
                "network schedule cannot rate capacity quantity".into(),
            ));
        }
        charge_progressive(quantity_micros, &self.brackets)
    }
}

fn charge_progressive(quantity: u64, brackets: &[ScalarBracket]) -> Result<i64, PricingError> {
    let mut numerator: u128 = 0;
    let mut denominator: u128 = 1;
    for bracket in brackets {
        let start = u64::try_from(bracket.range_start_quantity)
            .map_err(|_| PricingError("negative range start".into()))?;
        if quantity <= start {
            break;
        }
        let upper = bracket
            .range_end_quantity
            .map(|end| u64::try_from(end).map_err(|_| PricingError("negative range end".into())))
            .transpose()?
            .unwrap_or(quantity)
            .min(quantity);
        if upper <= start {
            continue;
        }
        let units = u128::from(upper - start);
        let price_numerator = u128::try_from(bracket.price_numerator_micro_units)
            .map_err(|_| PricingError("negative price numerator".into()))?;
        let price_denominator = u128::try_from(bracket.price_denominator_quantity)
            .map_err(|_| PricingError("invalid price denominator".into()))?;
        let left = numerator
            .checked_mul(price_denominator)
            .ok_or_else(|| PricingError("pricing arithmetic overflow".into()))?;
        let right = units
            .checked_mul(price_numerator)
            .ok_or_else(|| PricingError("pricing arithmetic overflow".into()))?
            .checked_mul(denominator)
            .ok_or_else(|| PricingError("pricing arithmetic overflow".into()))?;
        numerator = left
            .checked_add(right)
            .ok_or_else(|| PricingError("pricing arithmetic overflow".into()))?;
        denominator = denominator
            .checked_mul(price_denominator)
            .ok_or_else(|| PricingError("pricing arithmetic overflow".into()))?;
    }
    let mut rounded = numerator / denominator;
    if numerator % denominator != 0 {
        rounded = rounded
            .checked_add(1)
            .ok_or_else(|| PricingError("pricing arithmetic overflow".into()))?;
    }
    i64::try_from(rounded)
        .map_err(|_| PricingError("calculated charge exceeds BIGINT micro-unit capacity".into()))
}

#[derive(Debug, Clone, Default)]
pub struct CatalogSnapshot {
    pub(crate) versions_by_kind: HashMap<ChargeKind, Vec<Arc<PricingScheduleSnapshot>>>,
}

impl CatalogSnapshot {
    pub(crate) fn resolve(
        &self,
        kind: ChargeKind,
        zone_id: Uuid,
        at: DateTime<Utc>,
    ) -> Option<Arc<PricingScheduleSnapshot>> {
        let versions = self.versions_by_kind.get(&kind)?;
        versions
            .iter()
            .rev()
            .find(|version| {
                version.scope_type == "ZONE"
                    && version.zone_id == Some(zone_id)
                    && version.effective_from <= at
                    && version.effective_to.is_none_or(|end| at < end)
            })
            .cloned()
            .or_else(|| {
                versions
                    .iter()
                    .rev()
                    .find(|version| {
                        version.scope_type == "GLOBAL"
                            && version.effective_from <= at
                            && version.effective_to.is_none_or(|end| at < end)
                    })
                    .cloned()
            })
    }

    pub(crate) fn contains_version(&self, version_id: Uuid, checksum: &str) -> bool {
        self.versions_by_kind
            .values()
            .flatten()
            .any(|version| version.version_id == version_id && version.checksum == checksum)
    }

    pub(crate) fn find_version(&self, version_id: Uuid) -> Option<Arc<PricingScheduleSnapshot>> {
        self.versions_by_kind
            .values()
            .flatten()
            .find(|version| version.version_id == version_id)
            .cloned()
    }
}

pub struct BillingPricingLease {
    pub billing_run_id: Uuid,
    pub snapshot: Arc<PricingScheduleSnapshot>,
}
