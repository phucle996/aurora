use std::collections::HashMap;
use std::fmt::{Display, Formatter};
use std::sync::Arc;

use chrono::{DateTime, Utc};
use num_bigint::BigInt;
use num_rational::BigRational;
use num_traits::{ToPrimitive, Zero};
use uuid::Uuid;

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

/// Opaque, immutable modifier selected by the owning module. The PAYG kernel
/// applies it exactly but does not know which module policy produced it.
#[derive(Debug, Clone)]
pub struct RateAdjustmentSnapshot {
    pub id: Uuid,
    pub version_number: i32,
    pub checksum: String,
    pub numerator: i64,
    pub denominator: i64,
}

#[derive(Debug, Clone)]
pub struct PricingScheduleSnapshot {
    pub pricing_schedule_id: Uuid,
    pub version_id: Uuid,
    pub module_code: String,
    pub charge_kind_code: String,
    pub pricing_model: String,
    pub raw_input_unit: String,
    pub version_number: i32,
    pub effective_from: DateTime<Utc>,
    pub effective_to: Option<DateTime<Utc>>,
    pub checksum: String,
    pub brackets: Vec<ScalarBracket>,
}

impl PricingScheduleSnapshot {
    pub fn charge_micro_units(
        &self,
        quantity: u64,
        adjustment: Option<&RateAdjustmentSnapshot>,
    ) -> Result<i64, PricingError> {
        if self.pricing_model != "PROGRESSIVE_UNIT" {
            return Err(PricingError(format!(
                "unsupported pricing model {}",
                self.pricing_model
            )));
        }

        let mut total = BigRational::zero();
        for bracket in &self.brackets {
            let start = u64::try_from(bracket.range_start_quantity)
                .map_err(|_| PricingError("negative range start".into()))?;
            if quantity <= start {
                break;
            }
            let upper = bracket
                .range_end_quantity
                .map(|end| {
                    u64::try_from(end).map_err(|_| PricingError("negative range end".into()))
                })
                .transpose()?
                .unwrap_or(quantity)
                .min(quantity);
            if upper <= start {
                continue;
            }
            let numerator =
                BigInt::from(upper - start) * BigInt::from(bracket.price_numerator_micro_units);
            total += BigRational::new(numerator, BigInt::from(bracket.price_denominator_quantity));
        }

        if let Some(adjustment) = adjustment {
            if adjustment.numerator < 0 || adjustment.denominator <= 0 {
                return Err(PricingError("invalid rate adjustment".into()));
            }
            total *= BigRational::new(
                BigInt::from(adjustment.numerator),
                BigInt::from(adjustment.denominator),
            );
        }

        let rounded = total.ceil().to_integer();
        rounded.to_i64().ok_or_else(|| {
            PricingError("calculated charge exceeds BIGINT micro-unit capacity".into())
        })
    }
}

#[derive(Debug, Clone, Default)]
pub struct CatalogSnapshot {
    pub(crate) versions_by_kind: HashMap<String, Vec<Arc<PricingScheduleSnapshot>>>,
}

impl CatalogSnapshot {
    pub(crate) fn resolve(
        &self,
        charge_kind_code: &str,
        at: DateTime<Utc>,
    ) -> Option<Arc<PricingScheduleSnapshot>> {
        self.versions_by_kind
            .get(charge_kind_code)?
            .iter()
            .rev()
            .find(|version| {
                version.effective_from <= at && version.effective_to.is_none_or(|end| at < end)
            })
            .cloned()
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
    pub adjustment: Option<RateAdjustmentSnapshot>,
}

pub struct BillingRunCommand<'a> {
    pub source_module: &'a str,
    pub charge_kind_code: &'a str,
    pub source_report_id: Uuid,
    pub requested_start: DateTime<Utc>,
    pub requested_end: DateTime<Utc>,
    pub adjustment: Option<RateAdjustmentSnapshot>,
    pub fencing_token: i64,
}
