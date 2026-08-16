use super::*;

#[test]
fn storage_zone_adjustment_checksum_matches_go_publisher() {
    let zone_id = Uuid::parse_str("019f3d3e-998a-7894-9236-c5122634cb5a").unwrap();
    let effective_from = chrono::DateTime::parse_from_rfc3339("2026-08-15T06:30:00.000000Z")
        .unwrap()
        .with_timezone(&Utc);
    assert_eq!(
        zone_adjustment_checksum(zone_id, 1, effective_from, 105, 100),
        "ff35883f8f350cb70e85beda5f9b29e9cdd0c9179b9186fd20223508c040d580"
    );
}

fn valid_report() -> StorageUsageReportV1 {
    let window_end = Utc::now()
        .timestamp_millis()
        .div_euclid(HOURLY_WINDOW_MS)
        .saturating_mul(HOURLY_WINDOW_MS);
    let window_start = window_end.saturating_sub(HOURLY_WINDOW_MS);
    let sequence = u64::try_from(window_end.div_euclid(HOURLY_WINDOW_MS)).unwrap();
    let zone_id = Uuid::new_v4();
    let report_id = Uuid::new_v5(
        &REPORT_NAMESPACE,
        format!("{zone_id}:{window_start}:{window_end}:{sequence}").as_bytes(),
    );
    let mut report = StorageUsageReportV1 {
        schema_version: 1,
        report_id: report_id.to_string(),
        zone_id: zone_id.to_string(),
        window_start_unix_ms: window_start,
        window_end_unix_ms: window_end,
        sequence,
        correction: false,
        aggregates: vec![
            crate::service::storage::storage_usage_report_proto::StorageUsageAggregateV1 {
                resource_id: Uuid::new_v4().to_string(),
                upload_bytes: 0,
                download_bytes: 42,
                request_count: 1,
                resource_name: String::new(),
                storage_bytes: 0,
                storage_byte_hours: 0,
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

#[test]
fn recovers_identity_from_expired_report_for_dead_letter_persistence() {
    let mut report = valid_report();
    report.window_start_unix_ms = 3_600_000;
    report.window_end_unix_ms = 7_200_000;
    report.sequence = 2;
    let zone_id = Uuid::parse_str(&report.zone_id).unwrap();
    report.report_id = Uuid::new_v5(
        &REPORT_NAMESPACE,
        format!(
            "{}:{}:{}:{}",
            zone_id, report.window_start_unix_ms, report.window_end_unix_ms, report.sequence
        )
        .as_bytes(),
    )
    .to_string();
    let mut canonical = report.clone();
    canonical.report_sha256.clear();
    report.report_sha256 = Sha256::digest(canonical.encode_to_vec()).to_vec();
    let payload = report.encode_to_vec();
    assert_eq!(
        decode_report(&payload),
        Err("STORAGE_USAGE_REPORT_TIME_INVALID")
    );
    assert_eq!(
        recover_dead_report(&payload).map(|value| value.report_id),
        Some(report.report_id)
    );
}

#[test]
fn rejects_capacity_quantity_that_differs_from_observed_bytes() {
    let mut report = valid_report();
    let aggregate = &mut report.aggregates[0];
    aggregate.resource_id.clear();
    aggregate.resource_name = "ws-capacity".to_string();
    aggregate.download_bytes = 0;
    aggregate.request_count = 0;
    aggregate.storage_bytes = 1_001;
    aggregate.storage_byte_hours = 1;
    let mut canonical = report.clone();
    canonical.report_sha256.clear();
    report.report_sha256 = Sha256::digest(canonical.encode_to_vec()).to_vec();
    assert_eq!(
        decode_report(&report.encode_to_vec()),
        Err("STORAGE_USAGE_REPORT_CAPACITY_INVALID")
    );
}

#[sqlx::test(migrations = "../api/migrations")]
#[ignore = "requires an isolated PostgreSQL integration database"]
async fn storage_settlement_integration_debits_once_across_report_replay(pool: PgPool) {
    let zone_id = Uuid::new_v4();
    let owner_id = Uuid::new_v4();
    let wallet_id = Uuid::new_v4();
    let resource_id = Uuid::new_v4();
    let window_start = DateTime::parse_from_rfc3339("2026-08-15T06:00:00Z")
        .unwrap()
        .with_timezone(&Utc);
    let window_end = window_start + chrono::Duration::hours(1);

    sqlx::query(
        "INSERT INTO billing.wallets
         (id, owner_id, owner_type, currency, cash_balance, promotional_balance,
          overdraft_limit, status, restriction_reason)
         VALUES ($1,$2,'PERSONAL','USD',1000000,0,0,'ACTIVE',NULL)",
    )
    .bind(wallet_id)
    .bind(owner_id)
    .execute(&pool)
    .await
    .unwrap();
    let adjustment_effective_from = window_start - chrono::Duration::hours(1);
    let adjustment_checksum =
        zone_adjustment_checksum(zone_id, 1, adjustment_effective_from, 105, 100);
    sqlx::query(
        "INSERT INTO billing.storage_zone_price_adjustment_versions
         (id, zone_id, version_number, status, effective_from,
          multiplier_numerator, multiplier_denominator, checksum, change_reason, created_by)
         VALUES ($1,$2,1,'ACTIVE',$3,105,100,$4,'integration test',$5)",
    )
    .bind(Uuid::new_v4())
    .bind(zone_id)
    .bind(adjustment_effective_from)
    .bind(adjustment_checksum)
    .bind(Uuid::new_v4())
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        "INSERT INTO billing.resource_ownership_projection
         (id, resource_type, resource_id, resource_name, owner_id, owner_type,
          zone_id, ownership_version, effective_from, source_updated_at)
         VALUES ($1,'STORAGE_BUCKET',$2,$3,$4,'PERSONAL',$5,1,$6,$6)",
    )
    .bind(Uuid::new_v4())
    .bind(resource_id)
    .bind("ws-integration-bucket")
    .bind(owner_id)
    .bind(zone_id)
    .bind(window_start)
    .execute(&pool)
    .await
    .unwrap();

    let sequence =
        u64::try_from(window_end.timestamp_millis().div_euclid(HOURLY_WINDOW_MS)).unwrap();
    let report_id = Uuid::new_v5(
        &REPORT_NAMESPACE,
        format!(
            "{}:{}:{}:{}",
            zone_id,
            window_start.timestamp_millis(),
            window_end.timestamp_millis(),
            sequence
        )
        .as_bytes(),
    );
    let mut report = StorageUsageReportV1 {
        schema_version: 1,
        report_id: report_id.to_string(),
        zone_id: zone_id.to_string(),
        window_start_unix_ms: window_start.timestamp_millis(),
        window_end_unix_ms: window_end.timestamp_millis(),
        sequence,
        correction: false,
        aggregates: vec![
            crate::service::storage::storage_usage_report_proto::StorageUsageAggregateV1 {
                resource_id: resource_id.to_string(),
                upload_bytes: 0,
                download_bytes: 1_048_576,
                request_count: 1,
                resource_name: String::new(),
                storage_bytes: 0,
                storage_byte_hours: 0,
            },
        ],
        report_sha256: Vec::new(),
        correction_of_report_id: String::new(),
    };
    report.report_sha256 = Sha256::digest(report.encode_to_vec()).to_vec();
    let payload = report.encode_to_vec();
    let pricing_runtime = PricingRuntime::bootstrap(pool.clone()).await.unwrap();

    settle_report(&pool, &pricing_runtime, &report, &payload, 1)
        .await
        .unwrap();
    settle_report(&pool, &pricing_runtime, &report, &payload, 2)
        .await
        .unwrap();

    let (cash_balance, wallet_version): (i64, i64) =
        sqlx::query_as("SELECT cash_balance, version FROM billing.wallets WHERE id=$1")
            .bind(wallet_id)
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(cash_balance, 999_905);
    assert_eq!(wallet_version, 2);

    let ledger: (i64, i64) = sqlx::query_as(
        "SELECT COUNT(*), COALESCE(SUM(amount_micro_units),0)::bigint
         FROM billing.wallet_ledger_entries WHERE owner_id=$1",
    )
    .bind(owner_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(ledger, (1, -95));

    let report_status: String = sqlx::query_scalar(
        "SELECT status FROM billing.storage_usage_report_inbox WHERE report_id=$1",
    )
    .bind(report_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(report_status, "SETTLED");
}
