use super::*;

fn valid_report() -> StorageUsageReportV1 {
    let now = Utc::now().timestamp_millis();
    let mut report = StorageUsageReportV1 {
        schema_version: 1,
        report_id: Uuid::new_v4().to_string(),
        zone_id: Uuid::new_v4().to_string(),
        window_start_unix_ms: now.saturating_sub(60_000),
        window_end_unix_ms: now.saturating_sub(1_000),
        sequence: 1,
        correction: false,
        aggregates: vec![
            crate::engine::storage_usage_report_proto::StorageUsageAggregateV1 {
                resource_id: Uuid::new_v4().to_string(),
                upload_bytes: 0,
                download_bytes: 42,
                request_count: 1,
                resource_name: String::new(),
                storage_bytes: 0,
                storage_gb_hours_micros: 0,
            },
        ],
        report_sha256: Vec::new(),
        correction_of_report_id: String::new(),
    };
    let digest = Sha256::digest(report.encode_to_vec());
    report.report_sha256 = digest.to_vec();
    report
}

#[test]
fn accepts_canonical_report() {
    let report = valid_report();
    assert!(decode_report(&report.encode_to_vec()).is_ok());
}

#[test]
fn rejects_duplicate_resource_lines() {
    let mut report = valid_report();
    report.aggregates.push(report.aggregates[0].clone());
    assert_eq!(
        decode_report(&report.encode_to_vec()),
        Err("STORAGE_USAGE_REPORT_RESOURCE_DUPLICATE")
    );
    let mut canonical = report.clone();
    canonical.report_sha256.clear();
    report.report_sha256 = Sha256::digest(canonical.encode_to_vec()).to_vec();
    assert_eq!(
        decode_report(&report.encode_to_vec()),
        Err("STORAGE_USAGE_REPORT_RESOURCE_DUPLICATE")
    );
}

#[test]
fn accepts_correction_shape_but_settlement_rejects_policy() {
    let mut report = valid_report();
    report.correction = true;
    report.correction_of_report_id = Uuid::new_v4().to_string();
    let mut canonical = report.clone();
    canonical.report_sha256.clear();
    report.report_sha256 = Sha256::digest(canonical.encode_to_vec()).to_vec();
    assert!(decode_report(&report.encode_to_vec()).is_ok());
}
