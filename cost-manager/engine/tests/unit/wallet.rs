use super::*;

#[sqlx::test(migrations = "../api/migrations")]
#[ignore = "requires an isolated PostgreSQL integration database"]
async fn wallet_uses_promotion_then_bounded_overdraft_before_one_suspension(pool: sqlx::PgPool) {
    let owner_id = Uuid::new_v4();
    let wallet_id = Uuid::new_v4();
    let resource_id = Uuid::new_v4();
    let source_report_id = Uuid::new_v4();
    let run_id = Uuid::new_v4();
    let now = Utc::now();
    let (schedule_id, version_id, checksum): (Uuid, Uuid, String) = sqlx::query_as(
        "SELECT schedule.id, version.id, version.checksum
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
         VALUES ($1,$2,'PERSONAL','USD',0,10,100,'ACTIVE',NULL)",
    )
    .bind(wallet_id)
    .bind(owner_id)
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        "INSERT INTO billing.usage_settlement_runs
         (id,source_module,source_report_id,charge_kind_code,window_start,window_end,
          pricing_schedule_id,pricing_schedule_version_id,pricing_checksum,fencing_token,status)
         VALUES ($1,'storage',$2,'storage.network_out.byte',$3,$4,$5,$6,$7,1,'RUNNING')",
    )
    .bind(run_id)
    .bind(source_report_id)
    .bind(now - chrono::Duration::hours(1))
    .bind(now)
    .bind(schedule_id)
    .bind(version_id)
    .bind(&checksum)
    .execute(&pool)
    .await
    .unwrap();

    for (index, amount) in [10_i64, 90, 10, 1].into_iter().enumerate() {
        let ledger_id = Uuid::new_v4();
        let reference = format!("wallet-threshold-{index}");
        let mut tx = pool.begin().await.unwrap();
        let outcome = settle_usage_charge(
            &mut tx,
            UsageChargeCommand {
                ledger_entry_id: ledger_id,
                owner_id,
                owner_type: "PERSONAL",
                amount_micro_units: amount,
                module_code: "storage",
                charge_kind_code: "storage.network_out.byte",
                reference_id: &reference,
                description: "wallet threshold integration test",
                usage_settlement_run_id: run_id,
                pricing_schedule_id: schedule_id,
                pricing_schedule_version_id: version_id,
                pricing_checksum: &checksum,
                resource_id,
                resource_type: "STORAGE_BUCKET",
                usage_quantity: amount,
                usage_unit: "BYTE",
                occurred_at: now,
                source_evidence_hash:
                    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            },
        )
        .await
        .unwrap();
        assert!(matches!(outcome, UsageChargeOutcome::Settled));
        tx.commit().await.unwrap();

        let (cash, promotion, status): (i64, i64, String) = sqlx::query_as(
            "SELECT cash_balance,promotional_balance,status::text
             FROM billing.wallets WHERE id=$1",
        )
        .bind(wallet_id)
        .fetch_one(&pool)
        .await
        .unwrap();
        match index {
            0 => assert_eq!((cash, promotion, status.as_str()), (0, 0, "ACTIVE")),
            1 => assert_eq!((cash, promotion, status.as_str()), (-90, 0, "ACTIVE")),
            2 => assert_eq!((cash, promotion, status.as_str()), (-100, 0, "SUSPENDED")),
            3 => assert_eq!((cash, promotion, status.as_str()), (-101, 0, "SUSPENDED")),
            _ => unreachable!(),
        }
    }

    let admission_count: i64 = sqlx::query_scalar(
        "SELECT COUNT(*) FROM billing.wallet_admission_outbox WHERE wallet_id=$1",
    )
    .bind(wallet_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(admission_count, 1);
}
