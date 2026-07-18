use std::collections::HashMap;
use std::fmt::{Display, Formatter};
use std::sync::Arc;
use bigdecimal::{BigDecimal, RoundingMode, ToPrimitive};
use chrono::{DateTime, Utc};
use uuid::Uuid;

// [COMMENT]: ServiceType là enum kiểu định dạng tài nguyên được Rust Engine hiểu và kiểm soát kiểu dữ liệu an toàn.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum ServiceType {
    Storage,
    NetworkIn,
    NetworkOut,
    Vm,
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

impl ServiceType {
    // Parser exact match case, nếu không khớp bất kỳ kiểu cước nào sẽ fail-closed.
    pub fn parse(raw: &str) -> Result<Self, PricingError> {
        match raw.trim() {
            "STORAGE" => Ok(Self::Storage),
            "NETWORK_IN" => Ok(Self::NetworkIn),
            "NETWORK_OUT" => Ok(Self::NetworkOut),
            "VM" => Ok(Self::Vm),
            _ => Err(PricingError(format!("unknown service type: {raw}"))),
        }
    }

    pub fn as_str(&self) -> &'static str {
        match self {
            Self::Storage => "STORAGE",
            Self::NetworkIn => "NETWORK_IN",
            Self::NetworkOut => "NETWORK_OUT",
            Self::Vm => "VM",
        }
    }
}

#[derive(Debug, Clone)]
pub struct TierRange {
    pub id: Uuid,
    pub range_start: i64,
    pub range_end: i64,
    pub base_unit_price: i64,
}

#[derive(Debug, Clone)]
pub struct TierPricingSnapshot {
    pub tier_id: Uuid,
    pub tier_version_id: Uuid,
    pub version_number: i32,
    pub service_type: ServiceType,
    pub effective_from: DateTime<Utc>,
    pub effective_to: Option<DateTime<Utc>>,
    pub checksum: String,
    pub ranges: Vec<TierRange>,
}

impl TierPricingSnapshot {
    // [COMMENT]: Progressive charge: mỗi đoạn quantity nằm trong [start,end) nhân giá của range đó.
    pub fn charge_micro_units_for_bytes(&self, quantity_bytes: u64) -> Result<i64, PricingError> {
        let quantity_mb = BigDecimal::from(quantity_bytes) / BigDecimal::from(1_048_576_u64);
        let mut total_micro_units = BigDecimal::from(0);
        for tier_range in &self.ranges {
            let start = BigDecimal::from(tier_range.range_start);
            if quantity_mb <= start {
                break;
            }
            let upper = if tier_range.range_end == 0 {
                quantity_mb.clone()
            } else {
                let finite_end = BigDecimal::from(tier_range.range_end);
                if quantity_mb < finite_end {
                    quantity_mb.clone()
                } else {
                    finite_end
                }
            };
            let units = upper - start;
            if units > BigDecimal::from(0) {
                total_micro_units += units * BigDecimal::from(tier_range.base_unit_price);
            }
        }
        // [COMMENT]: Wallet/ledger dùng integer micro-unit; ceil một lần ở cuối để usage dương không biến thành free do truncation.
        total_micro_units
            .with_scale_round(0, RoundingMode::Ceiling)
            .to_i64()
            .ok_or_else(|| PricingError("calculated charge exceeds BIGINT micro-unit capacity".into()))
    }
}

#[derive(Debug, Clone, Default)]
pub struct CatalogSnapshot {
    pub(crate) versions_by_service: HashMap<ServiceType, Vec<Arc<TierPricingSnapshot>>>,
}

impl CatalogSnapshot {
    // [COMMENT]: Chọn version đã effective tại billing-run boundary; run đã tạo luôn resume bằng pinned ID.
    pub(crate) fn resolve(&self, service_type: ServiceType, at: DateTime<Utc>) -> Option<Arc<TierPricingSnapshot>> {
        self.versions_by_service
            .get(&service_type)?
            .iter()
            .rev()
            .find(|version| {
                version.effective_from <= at && version.effective_to.is_none_or(|end| at < end)
            })
            .cloned()
    }

    pub(crate) fn contains_version(&self, version_id: Uuid, checksum: &str) -> bool {
        self.versions_by_service
            .values()
            .flatten()
            .any(|version| version.tier_version_id == version_id && version.checksum == checksum)
    }

    pub(crate) fn find_version(&self, version_id: Uuid) -> Option<Arc<TierPricingSnapshot>> {
        self.versions_by_service
            .values()
            .flatten()
            .find(|version| version.tier_version_id == version_id)
            .cloned()
    }
}

pub struct BillingPricingLease {
    pub billing_run_id: Uuid,
    pub snapshot: Arc<TierPricingSnapshot>,
    pub window_start: DateTime<Utc>,
    pub window_end: DateTime<Utc>,
}
