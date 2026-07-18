use super::snapshot::{TierRange, TierPricingSnapshot, ServiceType};
use super::runtime::{validate_ranges, checksum};

use chrono::Utc;
use uuid::Uuid;

#[test]
fn validates_contiguous_ranges_and_checksum() {
    let ranges = vec![
        TierRange {
            id: Uuid::nil(),
            range_start: 0,
            range_end: 10,
            base_unit_price: 15,
        },
        TierRange {
            id: Uuid::nil(),
            range_start: 10,
            range_end: 0,
            base_unit_price: 12,
        },
    ];
    assert!(validate_ranges(&ranges).is_ok());
    assert_eq!(
        checksum("CODE", "STORAGE", &ranges),
        "7159ff73182d252b26bdeae4757467a99d2776a229f5a154de029c5cb0c47099"
    );
}

#[test]
fn rejects_gap_between_ranges() {
    let ranges = vec![
        TierRange {
            id: Uuid::nil(),
            range_start: 0,
            range_end: 10,
            base_unit_price: 15,
        },
        TierRange {
            id: Uuid::nil(),
            range_start: 11,
            range_end: 0,
            base_unit_price: 12,
        },
    ];
    assert!(validate_ranges(&ranges).is_err());
}

#[test]
fn progressive_charge_uses_each_range_without_float_rounding() {
    let snapshot = TierPricingSnapshot {
        tier_id: Uuid::nil(),
        tier_version_id: Uuid::nil(),
        version_number: 1,
        service_type: ServiceType::NetworkOut,
        effective_from: Utc::now(),
        effective_to: None,
        checksum: String::new(),
        ranges: vec![
            TierRange {
                id: Uuid::nil(),
                range_start: 0,
                range_end: 10,
                base_unit_price: 0,
            },
            TierRange {
                id: Uuid::nil(),
                range_start: 10,
                range_end: 0,
                base_unit_price: 1_000_000,
            },
        ],
    };
    assert_eq!(
        snapshot.charge_micro_units_for_bytes(15 * 1_048_576).unwrap(),
        5_000_000
    );
}

#[test]
fn positive_fractional_micro_unit_rounds_once_at_charge_boundary() {
    let snapshot = TierPricingSnapshot {
        tier_id: Uuid::nil(),
        tier_version_id: Uuid::nil(),
        version_number: 1,
        service_type: ServiceType::NetworkOut,
        effective_from: Utc::now(),
        effective_to: None,
        checksum: String::new(),
        ranges: vec![TierRange {
            id: Uuid::nil(),
            range_start: 0,
            range_end: 0,
            base_unit_price: 1,
        }],
    };
    assert_eq!(snapshot.charge_micro_units_for_bytes(1).unwrap(), 1);
}
