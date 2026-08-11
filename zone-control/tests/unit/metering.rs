use super::*;

fn report() -> StorageUsageReportV1 {
    let now = Utc::now().timestamp_millis();
    let mut report = StorageUsageReportV1 {
        schema_version: REPORT_SCHEMA_VERSION,
        report_id: Uuid::new_v4().to_string(),
        zone_id: Uuid::new_v4().to_string(),
        window_start_unix_ms: now - 60_000,
        window_end_unix_ms: now - 1_000,
        sequence: 1,
        correction: false,
        aggregates: vec![StorageUsageAggregateV1 {
            resource_id: Uuid::new_v4().to_string(),
            upload_bytes: 2,
            download_bytes: 42,
            request_count: 1,
            resource_name: String::new(),
            storage_bytes: 0,
            storage_gb_hours_micros: 0,
        }],
        report_sha256: vec![0; 32],
        correction_of_report_id: String::new(),
    };
    let mut unsigned = report.clone();
    unsigned.report_sha256.clear();
    report.report_sha256 = Sha256::digest(unsigned.encode_to_vec()).to_vec();
    report
}

#[test]
fn canonical_report_is_deterministic_and_zone_bound() {
    let report = report();
    let zone_id = Uuid::parse_str(&report.zone_id).unwrap();
    assert!(validate_report(&report, zone_id).is_ok());
    let mut canonical = report.clone();
    assert_eq!(
        canonical_report_payload(&mut canonical).unwrap(),
        report.encode_to_vec()
    );
}

#[test]
fn report_rejects_unsorted_resource_lines() {
    let mut report = report();
    report.aggregates[0].resource_id = "00000000-0000-0000-0000-000000000002".to_string();
    let second = StorageUsageAggregateV1 {
        resource_id: "00000000-0000-0000-0000-000000000001".to_string(),
        upload_bytes: 0,
        download_bytes: 1,
        request_count: 1,
        resource_name: String::new(),
        storage_bytes: 0,
        storage_gb_hours_micros: 0,
    };
    report.aggregates.push(second);
    assert_eq!(
        validate_report_shape(&report),
        Err("STORAGE_USAGE_REPORT_RESOURCE_ORDER_INVALID")
    );
}

#[test]
fn report_rejects_correction_for_initial_publisher() {
    let mut report = report();
    report.correction = true;
    assert_eq!(
        validate_report_shape(&report),
        Err("STORAGE_USAGE_REPORT_CONTRACT_INVALID")
    );
}

#[test]
fn report_rejects_cross_zone_identity() {
    let report = report();
    let expected_zone = Uuid::new_v4();
    assert_eq!(
        validate_report(&report, expected_zone),
        Err("STORAGE_USAGE_REPORT_ZONE_MISMATCH")
    );
}
