use super::runtime::{checksum, validate_brackets};
use super::snapshot::{CatalogSnapshot, ChargeKind, PricingScheduleSnapshot, ScalarBracket};
use chrono::Utc;
use std::collections::HashMap;
use std::sync::Arc;
use uuid::Uuid;

#[test]
fn validates_contiguous_brackets_and_checksum() {
    let brackets = vec![
        ScalarBracket {
            range_start_quantity: 0,
            range_end_quantity: Some(10),
            price_numerator_micro_units: 15,
            price_denominator_quantity: 1,
        },
        ScalarBracket {
            range_start_quantity: 10,
            range_end_quantity: None,
            price_numerator_micro_units: 12,
            price_denominator_quantity: 1,
        },
    ];
    assert!(validate_brackets(&brackets).is_ok());
    assert!(
        !checksum(
            "CODE",
            ChargeKind::StorageCapacity,
            "GLOBAL",
            None,
            "USD",
            1,
            Utc::now(),
            &brackets
        )
        .is_empty()
    );
}

#[test]
fn rejects_gap_between_brackets() {
    let brackets = vec![
        ScalarBracket {
            range_start_quantity: 0,
            range_end_quantity: Some(10),
            price_numerator_micro_units: 15,
            price_denominator_quantity: 1,
        },
        ScalarBracket {
            range_start_quantity: 11,
            range_end_quantity: None,
            price_numerator_micro_units: 12,
            price_denominator_quantity: 1,
        },
    ];
    assert!(validate_brackets(&brackets).is_err());
}

#[test]
fn progressive_charge_uses_raw_bytes_and_rational_denominator() {
    let snapshot = PricingScheduleSnapshot {
        pricing_schedule_id: Uuid::nil(),
        version_id: Uuid::nil(),
        charge_kind: ChargeKind::StorageNetworkOut,
        scope_type: "GLOBAL".into(),
        zone_id: None,
        version_number: 1,
        effective_from: Utc::now(),
        effective_to: None,
        checksum: String::new(),
        brackets: vec![
            ScalarBracket {
                range_start_quantity: 0,
                range_end_quantity: Some(10 * 1_048_576),
                price_numerator_micro_units: 0,
                price_denominator_quantity: 1,
            },
            ScalarBracket {
                range_start_quantity: 10 * 1_048_576,
                range_end_quantity: None,
                price_numerator_micro_units: 1_000_000,
                price_denominator_quantity: 1_048_576,
            },
        ],
    };
    assert_eq!(
        snapshot
            .charge_micro_units_for_bytes(15 * 1_048_576)
            .unwrap(),
        5_000_000
    );
}

#[test]
fn storage_gb_hour_fixed_point_uses_decimal_micro_units() {
    let snapshot = PricingScheduleSnapshot {
        pricing_schedule_id: Uuid::nil(),
        version_id: Uuid::nil(),
        charge_kind: ChargeKind::StorageCapacity,
        scope_type: "GLOBAL".into(),
        zone_id: None,
        version_number: 1,
        effective_from: Utc::now(),
        effective_to: None,
        checksum: String::new(),
        brackets: vec![ScalarBracket {
            range_start_quantity: 0,
            range_end_quantity: None,
            price_numerator_micro_units: 12_000,
            price_denominator_quantity: 1_000_000,
        }],
    };
    assert_eq!(
        snapshot
            .charge_micro_units_for_storage_gb_hours_micros(1_000_000)
            .unwrap(),
        12_000
    );
}

#[test]
fn zone_schedule_wins_over_global_at_the_same_boundary() {
    let zone_id = Uuid::new_v4();
    let global = Arc::new(PricingScheduleSnapshot {
        pricing_schedule_id: Uuid::new_v4(),
        version_id: Uuid::new_v4(),
        charge_kind: ChargeKind::StorageCapacity,
        scope_type: "GLOBAL".into(),
        zone_id: None,
        version_number: 1,
        effective_from: Utc::now() - chrono::Duration::hours(1),
        effective_to: None,
        checksum: "global".into(),
        brackets: vec![ScalarBracket {
            range_start_quantity: 0,
            range_end_quantity: None,
            price_numerator_micro_units: 1,
            price_denominator_quantity: 1,
        }],
    });
    let zone = Arc::new(PricingScheduleSnapshot {
        pricing_schedule_id: Uuid::new_v4(),
        version_id: Uuid::new_v4(),
        charge_kind: ChargeKind::StorageCapacity,
        scope_type: "ZONE".into(),
        zone_id: Some(zone_id),
        version_number: 1,
        effective_from: Utc::now() - chrono::Duration::hours(1),
        effective_to: None,
        checksum: "zone".into(),
        brackets: vec![ScalarBracket {
            range_start_quantity: 0,
            range_end_quantity: None,
            price_numerator_micro_units: 2,
            price_denominator_quantity: 1,
        }],
    });
    let mut versions = HashMap::new();
    versions.insert(ChargeKind::StorageCapacity, vec![global, zone.clone()]);
    let catalog = CatalogSnapshot {
        versions_by_kind: versions,
    };
    let selected = catalog
        .resolve(ChargeKind::StorageCapacity, zone_id, Utc::now())
        .expect("a schedule should resolve");
    assert_eq!(selected.version_id, zone.version_id);
}
