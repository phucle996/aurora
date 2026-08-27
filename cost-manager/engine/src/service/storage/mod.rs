// [COMMENT]: Khai báo module billing cho dịch vụ storage
#[allow(dead_code)]
pub mod storage_usage_report_proto {
    include!(concat!(env!("OUT_DIR"), "/aurora.storage.metering.v1.rs"));
}

pub mod pending_activation_reconcile;
pub mod usage_report_settlement;

pub(super) fn zone_adjustment_checksum(
    zone_id: Uuid,
    version_number: i32,
    effective_from: DateTime<Utc>,
    numerator: i64,
    denominator: i64,
) -> String {
    let mut hasher = Sha256::new();
    for value in [
        zone_id.to_string(),
        version_number.to_string(),
        effective_from.to_rfc3339_opts(chrono::SecondsFormat::Micros, true),
        numerator.to_string(),
        denominator.to_string(),
    ] {
        hasher.update((value.len() as u64).to_be_bytes());
        hasher.update(value.as_bytes());
    }
    format!("{:x}", hasher.finalize())
}
use chrono::{DateTime, Utc};
use sha2::{Digest, Sha256};
use uuid::Uuid;
