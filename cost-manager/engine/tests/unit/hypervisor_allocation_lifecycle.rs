use super::*;
use chrono::Timelike;

fn activation() -> HypervisorAllocationChangedV1 {
    HypervisorAllocationChangedV1 {
        schema_version: 1,
        event_id: Uuid::now_v7().as_bytes().to_vec(),
        event_type: "ACTIVATE".to_string(),
        resource_id: Uuid::now_v7().as_bytes().to_vec(),
        zone_id: Uuid::now_v7().as_bytes().to_vec(),
        source_version: 1,
        effective_at_unix_ms: Utc::now().timestamp_millis(),
        cpu_cores: 4,
        memory_mib: 8192,
        disk_gib: 160,
        gpu_sku: String::new(),
        gpu_count: 0,
        source_job_id: Uuid::now_v7().as_bytes().to_vec(),
    }
}

#[test]
fn accepts_bounded_integer_allocation() {
    let payload = activation().encode_to_vec();
    let decoded = decode_event(&payload).unwrap();
    assert_eq!(decoded.cpu_cores, 4);
    assert_eq!(decoded.memory_mib, 8192);
    assert_eq!(decoded.disk_gib, 160);
}

#[test]
fn rejects_gpu_that_is_not_backed_by_a_sku() {
    let mut event = activation();
    event.gpu_count = 1;
    assert_eq!(
        decode_event(&event.encode_to_vec()).unwrap_err(),
        "ALLOCATION_LIMITS_INVALID"
    );
}

#[sqlx::test(migrations = "../api/migrations")]
#[ignore = "requires an isolated PostgreSQL integration database"]
async fn quarantines_revision_that_would_rewrite_a_settled_window(pool: PgPool) {
    let window_start = Utc::now()
        .checked_sub_signed(chrono::Duration::hours(2))
        .unwrap()
        .with_minute(0)
        .unwrap()
        .with_second(0)
        .unwrap()
        .with_nanosecond(0)
        .unwrap();
    let mut active = activation();
    active.effective_at_unix_ms = window_start.timestamp_millis();
    let payload = active.encode_to_vec();
    assert!(apply_event(&pool, &active, &payload).await.is_ok());

    let resource_id = Uuid::from_slice(&active.resource_id).unwrap();
    let zone_id = Uuid::from_slice(&active.zone_id).unwrap();
    let shard_id: i32 = sqlx::query_scalar(
        "SELECT mod((hashtextextended($1,0) & 9223372036854775807), $2::bigint)::int",
    )
    .bind(resource_id.to_string())
    .bind(i64::from(ALLOCATION_SHARD_COUNT))
    .fetch_one(&pool)
    .await
    .unwrap();
    sqlx::query(
        "INSERT INTO billing.hypervisor_allocation_windows
         (id,zone_id,shard_id,window_start,window_end,status,settled_at)
         VALUES ($1,$2,$3,$4,$5,'SETTLED',NOW())",
    )
    .bind(Uuid::new_v4())
    .bind(zone_id)
    .bind(shard_id)
    .bind(window_start)
    .bind(window_start + chrono::Duration::hours(1))
    .execute(&pool)
    .await
    .unwrap();

    let mut revision = active.clone();
    revision.event_id = Uuid::now_v7().as_bytes().to_vec();
    revision.event_type = "REVISE".to_string();
    revision.source_version = 2;
    revision.effective_at_unix_ms =
        (window_start + chrono::Duration::minutes(30)).timestamp_millis();
    revision.cpu_cores = 8;
    let revision_payload = revision.encode_to_vec();
    let result = apply_event(&pool, &revision, &revision_payload).await;
    assert!(matches!(
        result,
        Err(ApplyFailure::Integrity(reason))
            if reason == "ALLOCATION_EVENT_AFTER_SETTLED_WINDOW"
    ));

    let (version, effective_to): (i64, Option<DateTime<Utc>>) = sqlx::query_as(
        "SELECT allocation_version,effective_to
         FROM billing.hypervisor_allocation_intervals WHERE resource_id=$1",
    )
    .bind(resource_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(version, 1);
    assert!(effective_to.is_none());
}
