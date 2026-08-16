use super::runtime::{checksum, validate_brackets};
use super::snapshot::{
    CatalogSnapshot, PricingScheduleSnapshot, RateAdjustmentSnapshot, ScalarBracket,
};
use chrono::Utc;
use std::collections::HashMap;
use std::sync::Arc;
use uuid::Uuid;

fn snapshot(brackets: Vec<ScalarBracket>) -> PricingScheduleSnapshot {
    PricingScheduleSnapshot {
        pricing_schedule_id: Uuid::new_v4(),
        version_id: Uuid::new_v4(),
        module_code: "test-module".into(),
        charge_kind_code: "test.quantity".into(),
        pricing_model: "PROGRESSIVE_UNIT".into(),
        raw_input_unit: "unit".into(),
        version_number: 1,
        effective_from: Utc::now() - chrono::Duration::hours(1),
        effective_to: None,
        checksum: String::new(),
        brackets,
    }
}

#[test]
fn validates_contiguous_brackets_and_checksum() {
    let brackets = vec![ScalarBracket {
        range_start_quantity: 0,
        range_end_quantity: None,
        price_numerator_micro_units: 15,
        price_denominator_quantity: 1,
    }];
    assert!(validate_brackets(&brackets).is_ok());
    assert!(
        !checksum(
            "CODE",
            "test.quantity",
            "PROGRESSIVE_UNIT",
            "USD",
            1,
            Utc::now(),
            &brackets,
        )
        .is_empty()
    );
}

#[test]
fn checksum_matches_global_base_seed_and_go_publisher() {
    let brackets = vec![
        ScalarBracket {
            range_start_quantity: 0,
            range_end_quantity: Some(50_000_000_000),
            price_numerator_micro_units: 15_000,
            price_denominator_quantity: 1_000_000_000,
        },
        ScalarBracket {
            range_start_quantity: 50_000_000_000,
            range_end_quantity: None,
            price_numerator_micro_units: 12_000,
            price_denominator_quantity: 1_000_000_000,
        },
    ];
    let effective_from = chrono::DateTime::parse_from_rfc3339("2026-07-18T10:11:25.589234Z")
        .unwrap()
        .with_timezone(&Utc);
    assert_eq!(
        checksum(
            "storage-capacity-payg",
            "storage.capacity.gb_hour",
            "PROGRESSIVE_UNIT",
            "USD",
            1,
            effective_from,
            &brackets,
        ),
        "625d0d50cce646f5fbd2f988226d69f9d50f55c99cee9262990e060ba3f702d9"
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
fn progressive_charge_uses_exact_rationals() {
    let pricing = snapshot(vec![ScalarBracket {
        range_start_quantity: 0,
        range_end_quantity: None,
        price_numerator_micro_units: 1_000_000,
        price_denominator_quantity: 1_048_576,
    }]);
    assert_eq!(
        pricing.charge_micro_units(5 * 1_048_576, None).unwrap(),
        5_000_000
    );
}

#[test]
fn modifier_is_applied_before_the_single_final_ceiling() {
    let pricing = snapshot(vec![ScalarBracket {
        range_start_quantity: 0,
        range_end_quantity: None,
        price_numerator_micro_units: 1,
        price_denominator_quantity: 2,
    }]);
    let adjustment = RateAdjustmentSnapshot {
        id: Uuid::new_v4(),
        version_number: 1,
        checksum: "adjustment".into(),
        numerator: 1,
        denominator: 2,
    };
    // Exact result is 1/4 and therefore rounds once to 1. Rounding the base
    // first and multiplying later would incorrectly produce 1/2.
    assert_eq!(pricing.charge_micro_units(1, Some(&adjustment)).unwrap(), 1);
}

#[test]
fn catalog_resolves_only_the_global_base_schedule() {
    let base = Arc::new(snapshot(vec![ScalarBracket {
        range_start_quantity: 0,
        range_end_quantity: None,
        price_numerator_micro_units: 1,
        price_denominator_quantity: 1,
    }]));
    let mut versions = HashMap::new();
    versions.insert("test.quantity".to_string(), vec![base.clone()]);
    let catalog = CatalogSnapshot {
        versions_by_kind: versions,
    };
    let selected = catalog
        .resolve("test.quantity", Utc::now())
        .expect("a schedule should resolve");
    assert_eq!(selected.version_id, base.version_id);
}
