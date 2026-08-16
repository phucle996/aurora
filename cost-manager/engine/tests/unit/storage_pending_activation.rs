use super::*;

#[sqlx::test(migrations = "../api/migrations")]
#[ignore = "requires an isolated PostgreSQL integration database"]
async fn resumes_remaining_lines_after_credit_exhaustion_crosses_batch_boundary(pool: PgPool) {
    let owner_id = Uuid::new_v4();
    let wallet_id = Uuid::new_v4();
    let zone_id = Uuid::new_v4();
    let resource_id = Uuid::new_v4();
    let run_id = Uuid::new_v4();
    let source_report_id = Uuid::new_v4();
    let metering_hour = Utc::now() - chrono::Duration::hours(1);
    let (schedule_id, version_id, checksum): (Uuid, Uuid, String) = sqlx::query_as(
        "SELECT schedule.id,version.id,version.checksum
         FROM billing.pricing_schedules schedule
         JOIN billing.pricing_schedule_versions version
           ON version.pricing_schedule_id=schedule.id
         WHERE schedule.charge_kind_code='storage.network_out.byte'
         ORDER BY version.version_number DESC LIMIT 1",
    )
    .fetch_one(&pool)
    .await
    .unwrap();

    sqlx::query(
        "INSERT INTO billing.wallets
         (id,owner_id,owner_type,currency,cash_balance,promotional_balance,
          overdraft_limit,status,restriction_reason)
         VALUES ($1,$2,'PERSONAL','USD',0,0,100,'PENDING_ACTIVATION','NOT_ACTIVATED')",
    )
    .bind(wallet_id)
    .bind(owner_id)
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        "INSERT INTO billing.storage_pending_activation_reconcile
         (wallet_id,owner_id,owner_type,target_wallet_version,status)
         VALUES ($1,$2,'PERSONAL',1,'PENDING')",
    )
    .bind(wallet_id)
    .bind(owner_id)
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        "INSERT INTO billing.resource_ownership_projection
         (id,resource_type,resource_id,resource_name,owner_id,owner_type,zone_id,
          ownership_version,effective_from,source_updated_at)
         VALUES ($1,'STORAGE_BUCKET',$2,$3,$4,'PERSONAL',$5,1,$6,$6)",
    )
    .bind(Uuid::new_v4())
    .bind(resource_id)
    .bind(resource_id.to_string())
    .bind(owner_id)
    .bind(zone_id)
    .bind(metering_hour - chrono::Duration::hours(1))
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        "INSERT INTO billing.usage_settlement_runs
         (id,source_module,source_report_id,charge_kind_code,window_start,window_end,
          pricing_schedule_id,pricing_schedule_version_id,pricing_checksum,fencing_token,status)
         VALUES ($1,'storage',$2,'storage.network_out.byte',$3,$4,$5,$6,$7,1,'RETRYING')",
    )
    .bind(run_id)
    .bind(source_report_id)
    .bind(metering_hour - chrono::Duration::hours(1))
    .bind(metering_hour)
    .bind(schedule_id)
    .bind(version_id)
    .bind(&checksum)
    .execute(&pool)
    .await
    .unwrap();

    sqlx::query(
        "WITH generated AS MATERIALIZED (
             SELECT gen_random_uuid() AS report_id,gen_random_uuid() AS line_id,ordinal
             FROM generate_series(1,501) ordinal
         ), inserted_reports AS (
             INSERT INTO billing.storage_usage_report_inbox
             (report_id,zone_id,window_start,window_end,sequence,payload_sha256,payload,status)
             SELECT report_id,$1,$2 - INTERVAL '1 hour',$2,ordinal,
                    decode(repeat('00',32),'hex'),''::bytea,'UNRATED'
             FROM generated RETURNING report_id
         )
         INSERT INTO billing.storage_usage_line_inbox
         (line_id,report_id,zone_id,resource_id,direction,usage_quantity,usage_unit,
          request_count,owner_id,owner_type,module_code,charge_kind_code,
          usage_settlement_run_id,pricing_schedule_version_id,pricing_checksum,status,reason)
         SELECT generated.line_id,generated.report_id,$1,$3,'NETWORK_OUT',1048576,'BYTE',1,
                $4,'PERSONAL','storage','storage.network_out.byte',$5,$6,$7,
                'UNRATED','WALLET_PENDING_ACTIVATION'
         FROM generated JOIN inserted_reports USING (report_id)",
    )
    .bind(zone_id)
    .bind(metering_hour)
    .bind(resource_id)
    .bind(owner_id)
    .bind(run_id)
    .bind(version_id)
    .bind(&checksum)
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        "INSERT INTO billing.unrated_usage
         (id,module_code,charge_kind_code,resource_type,resource_id,resource_name,
          metering_hour,usage_quantity,usage_unit,reason,source_report_id,
          source_evidence_hash,pricing_schedule_version_id)
         SELECT line.line_id,'storage','storage.network_out.byte','STORAGE_BUCKET',
                line.resource_id,line.resource_id::text,$1,1048576,'BYTE',
                'WALLET_PENDING_ACTIVATION',line.report_id,repeat('a',64),$2
         FROM billing.storage_usage_line_inbox line WHERE line.owner_id=$3",
    )
    .bind(metering_hour)
    .bind(version_id)
    .bind(owner_id)
    .execute(&pool)
    .await
    .unwrap();

    let runtime = PricingRuntime::bootstrap(pool.clone()).await.unwrap();
    reconcile_batch(&pool, &runtime).await.unwrap();

    let first_status: String = sqlx::query_scalar(
        "SELECT status FROM billing.storage_pending_activation_reconcile WHERE wallet_id=$1",
    )
    .bind(wallet_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    let first_ledger_count: i64 =
        sqlx::query_scalar("SELECT COUNT(*) FROM billing.wallet_ledger_entries WHERE wallet_id=$1")
            .bind(wallet_id)
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(first_status, "PENDING");
    assert_eq!(first_ledger_count, 500);

    reconcile_batch(&pool, &runtime).await.unwrap();
    let (request_status, wallet_status, cash_balance): (String, String, i64) = sqlx::query_as(
        "SELECT request.status,wallet.status::text,wallet.cash_balance
         FROM billing.storage_pending_activation_reconcile request
         JOIN billing.wallets wallet ON wallet.id=request.wallet_id
         WHERE request.wallet_id=$1",
    )
    .bind(wallet_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    let (ledger_count, admission_count, unresolved_count): (i64, i64, i64) = sqlx::query_as(
        "SELECT
           (SELECT COUNT(*) FROM billing.wallet_ledger_entries WHERE wallet_id=$1),
           (SELECT COUNT(*) FROM billing.wallet_admission_outbox WHERE wallet_id=$1),
           (SELECT COUNT(*) FROM billing.unrated_usage WHERE status <> 'RESOLVED')",
    )
    .bind(wallet_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(
        (request_status.as_str(), wallet_status.as_str()),
        ("COMPLETED", "SUSPENDED")
    );
    assert_eq!(cash_balance, -45_090);
    assert_eq!(
        (ledger_count, admission_count, unresolved_count),
        (501, 1, 0)
    );
}
