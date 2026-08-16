pub mod allocation_lifecycle;
pub mod hourly_allocation_settlement;
pub mod network_usage_settlement;
mod network_usage_stream;

pub mod allocation_proto {
    include!(concat!(env!("OUT_DIR"), "/aurora.hypervisor.billing.v1.rs"));
}

pub mod network_usage_proto {
    include!(concat!(
        env!("OUT_DIR"),
        "/aurora.hypervisor.metering.v1.rs"
    ));
}

pub(super) const ALLOCATION_SHARD_COUNT: i32 = 16;

pub(super) fn zone_adjustment_checksum(
    zone_id: uuid::Uuid,
    version_number: i32,
    effective_from: chrono::DateTime<chrono::Utc>,
    numerator: i64,
    denominator: i64,
) -> String {
    use sha2::{Digest, Sha256};
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
