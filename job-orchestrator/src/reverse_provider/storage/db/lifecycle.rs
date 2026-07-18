use prost::Message;
use uuid::Uuid;

// [COMMENT]: Import generated Protobuf structs từ resource_lifecycle.proto
pub mod lifecycle_proto {
    include!(concat!(env!("OUT_DIR"), "/resource_lifecycle.rs"));
}

// [COMMENT]: Namespace UUID dùng để sinh deterministic event_id theo RFC 4122 UUIDv5.
// event_id = UUID_v5(LIFECYCLE_NAMESPACE, source_job_id_bytes || event_type_bytes)
// Đảm bảo mọi retry đều sinh đúng một event_id — idempotent publish lên JetStream.
const LIFECYCLE_NAMESPACE: Uuid = Uuid::from_bytes([
    0x6b, 0x18, 0x4e, 0x2a, 0x9f, 0x3c, 0x5d, 0x71, 0x8a, 0x2b, 0x1c, 0x4f, 0x6e, 0x7d, 0x0e, 0x3f,
]);

/// [COMMENT]: Sinh deterministic event_id từ source_job_id và event_type.
/// Đảm bảo retry không tạo event_id khác → JetStream Nats-Msg-Id dedup hoạt động đúng.
fn make_event_id(source_job_id: Uuid, event_type: &str) -> Uuid {
    let mut seed = source_job_id.as_bytes().to_vec();
    seed.extend_from_slice(event_type.as_bytes());
    Uuid::new_v5(&LIFECYCLE_NAMESPACE, &seed)
}

/// [COMMENT]: Tham số đầu vào cho insert lifecycle event.
/// owner_id và owner_type phải được derive từ DB ngay tại thời điểm xử lý
/// (personal_workspaces.owner_id hoặc tenant_buckets.tenant_id) — không dùng outbox.user_id.
pub struct LifecycleEventParams<'a> {
    pub source_job_id: Uuid,
    pub resource_id: Uuid,
    pub resource_type: &'a str,
    pub resource_name: &'a str,
    pub owner_id: Uuid,
    pub owner_type: &'a str, // "PERSONAL" | "TENANT"
    pub zone_id: Uuid,
    pub source_version: i64,
    pub effective_at: chrono::DateTime<chrono::Utc>,
    pub traceparent: Option<&'a str>,
}

/// [COMMENT]: Insert RESOURCE_CREATED lifecycle event vào storage.resource_lifecycle_events.
/// Phải được gọi trong cùng transaction với UPDATE job outbox thành SUCCEEDED.
/// Nếu record đã tồn tại (retry) — ON CONFLICT DO NOTHING đảm bảo idempotency.
pub async fn insert_resource_created(
    tx: &tokio_postgres::Transaction<'_>,
    params: LifecycleEventParams<'_>,
) -> Result<(), tokio_postgres::Error> {
    let event_type = "RESOURCE_CREATED";
    let event_id = make_event_id(params.source_job_id, event_type);
    let row_id = Uuid::new_v4();

    // [COMMENT]: Build Protobuf payload — tuyệt đối không có secret key hay policy JSON
    let proto_event = lifecycle_proto::ResourceLifecycleEventV1 {
        event_id: event_id.as_bytes().to_vec(),
        event_type: event_type.to_string(),
        schema_version: 1,
        resource_id: params.resource_id.as_bytes().to_vec(),
        resource_type: params.resource_type.to_string(),
        resource_name: params.resource_name.to_string(),
        owner_id: params.owner_id.as_bytes().to_vec(),
        owner_type: params.owner_type.to_string(),
        zone_id: params.zone_id.as_bytes().to_vec(),
        source_version: params.source_version,
        effective_at: params.effective_at.to_rfc3339(),
        source_job_id: params.source_job_id.as_bytes().to_vec(),
        traceparent: params.traceparent.unwrap_or("").to_string(),
    };

    let mut payload_buf = Vec::new();
    proto_event
        .encode(&mut payload_buf)
        .expect("protobuf encode should not fail for valid message");

    // [COMMENT]: ON CONFLICT DO NOTHING để idempotent trong trường hợp retry TX
    tx.execute(
        "INSERT INTO storage.resource_lifecycle_events \
            (id, event_id, event_type, schema_version, \
             resource_id, resource_type, resource_name, \
             owner_id, owner_type, zone_id, \
             source_version, effective_at, \
             source_job_id, traceparent, payload) \
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15) \
         ON CONFLICT (event_id) DO NOTHING",
        &[
            &row_id,
            &event_id,
            &event_type,
            &1_i32,
            &params.resource_id,
            &params.resource_type,
            &params.resource_name,
            &params.owner_id,
            &params.owner_type,
            &params.zone_id,
            &params.source_version,
            &params.effective_at,
            &params.source_job_id,
            &params.traceparent.unwrap_or(""),
            &payload_buf,
        ],
    )
    .await?;

    Ok(())
}

/// [COMMENT]: Insert RESOURCE_DELETED lifecycle event vào storage.resource_lifecycle_events.
/// Phải được gọi trong cùng transaction với DELETE bucket/credentials và UPDATE job outbox.
/// Owner/name/zone phải được capture TRƯỚC khi DELETE bucket record — sau khi DELETE không còn data.
pub async fn insert_resource_deleted(
    tx: &tokio_postgres::Transaction<'_>,
    params: LifecycleEventParams<'_>,
) -> Result<(), tokio_postgres::Error> {
    let event_type = "RESOURCE_DELETED";
    let event_id = make_event_id(params.source_job_id, event_type);
    let row_id = Uuid::new_v4();

    // [COMMENT]: Build Protobuf payload — tuyệt đối không có secret key hay policy JSON
    let proto_event = lifecycle_proto::ResourceLifecycleEventV1 {
        event_id: event_id.as_bytes().to_vec(),
        event_type: event_type.to_string(),
        schema_version: 1,
        resource_id: params.resource_id.as_bytes().to_vec(),
        resource_type: params.resource_type.to_string(),
        resource_name: params.resource_name.to_string(),
        owner_id: params.owner_id.as_bytes().to_vec(),
        owner_type: params.owner_type.to_string(),
        zone_id: params.zone_id.as_bytes().to_vec(),
        source_version: params.source_version,
        effective_at: params.effective_at.to_rfc3339(),
        source_job_id: params.source_job_id.as_bytes().to_vec(),
        traceparent: params.traceparent.unwrap_or("").to_string(),
    };

    let mut payload_buf = Vec::new();
    proto_event
        .encode(&mut payload_buf)
        .expect("protobuf encode should not fail for valid message");

    // [COMMENT]: ON CONFLICT DO NOTHING để idempotent trong trường hợp retry TX
    tx.execute(
        "INSERT INTO storage.resource_lifecycle_events \
            (id, event_id, event_type, schema_version, \
             resource_id, resource_type, resource_name, \
             owner_id, owner_type, zone_id, \
             source_version, effective_at, \
             source_job_id, traceparent, payload) \
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15) \
         ON CONFLICT (event_id) DO NOTHING",
        &[
            &row_id,
            &event_id,
            &event_type,
            &1_i32,
            &params.resource_id,
            &params.resource_type,
            &params.resource_name,
            &params.owner_id,
            &params.owner_type,
            &params.zone_id,
            &params.source_version,
            &params.effective_at,
            &params.source_job_id,
            &params.traceparent.unwrap_or(""),
            &payload_buf,
        ],
    )
    .await?;

    Ok(())
}
