use crate::observability::logger::Logger;

/// [COMMENT]: Cập nhật trạng thái Zone trực tiếp trong PostgreSQL (Direct DB Write - Bypass CP).
/// Áp dụng ràng buộc State Machine nghiêm ngặt để tránh cache RAM stale của JO ghi đè cấu hình thủ công SRE.
/// Trả về true nếu ghi thành công, false nếu DB Guard từ chối (chuyển trạng thái không hợp lệ).
pub async fn update_zone_status(
    pg_client: &tokio_postgres::Client,
    zone_id: &str,
    status: &str,
) -> Result<bool, Box<dyn std::error::Error>> {
    // [COMMENT]: Thực thi lệnh UPDATE kèm ràng buộc State Machine (hierarchy.zone_status enum).
    // Chỉ cho phép chuyển giữa active/draining với nhau, và chuyển sang disabled từ mọi trạng thái vận hành.
    // Các trạng thái SRE-owned (planned, maintenance) không được tự động chuyển.
    let rows_affected = pg_client
        .execute(
            "UPDATE hierarchy.zones \
             SET status = $1::text::hierarchy.zone_status, updated_at = NOW() \
             WHERE id = $2::text::uuid \
               AND status != $1::text::hierarchy.zone_status \
               AND ( \
                   ($1::text IN ('active', 'draining') AND status::text IN ('active', 'draining')) \
                   OR \
                   ($1::text = 'disabled' AND status::text IN ('active', 'draining', 'maintenance')) \
               )",
            &[&status, &zone_id],
        )
        .await?;

    if rows_affected > 0 {
        Logger::sys_info(
            "zone_db.update_zone",
            &format!(
                "Đã cập nhật trạng thái Zone {} sang status: '{}' trực tiếp trong DB SoT.",
                zone_id, status
            ),
        );
        Ok(true)
    } else {
        Ok(false)
    }
}

pub struct ZonePolicyState {
    pub status: String,
    pub mail_enabled: bool,
    pub storage_enabled: bool,
}

/// Reads the complete policy input in one round-trip. Report workers do not
/// cache SRE-owned lifecycle/desired state across requests.
pub async fn query_zone_policy_state(
    pg_client: &tokio_postgres::Client,
    zone_id: &str,
) -> Result<ZonePolicyState, Box<dyn std::error::Error + Send + Sync>> {
    let row = pg_client
        .query_opt(
            "SELECT z.status::text, \
                    COALESCE(BOOL_OR(s.service_type::text = 'mail' AND s.desired_state), FALSE), \
                    COALESCE(BOOL_OR(s.service_type::text = 'storage' AND s.desired_state), FALSE) \
             FROM hierarchy.zones z \
             LEFT JOIN hierarchy.zone_services s ON s.zone_id = z.id \
             WHERE z.id = $1::text::uuid \
             GROUP BY z.status",
            &[&zone_id],
        )
        .await?;
    Ok(row.map_or(
        ZonePolicyState {
            status: "disabled".to_string(),
            mail_enabled: false,
            storage_enabled: false,
        },
        |row| ZonePolicyState {
            status: row.get(0),
            mail_enabled: row.get(1),
            storage_enabled: row.get(2),
        },
    ))
}

/// [COMMENT]: Truy vấn đầy đủ metadata của Zone (Status & trạng thái enabled của tất cả Service).
/// Được gọi bởi query.rs (PubSub metadata responder) để phản hồi Dataplane reconciliation request.
pub async fn query_zone_metadata(
    pg_client: &tokio_postgres::Client,
    zone_id: &str,
) -> Result<(String, std::collections::HashMap<String, bool>), Box<dyn std::error::Error>> {
    // [COMMENT]: 1. Lấy trạng thái Zone từ bảng hierarchy.zones, cast sang text để deserialize
    let zone_rows = pg_client
        .query(
            "SELECT status::text FROM hierarchy.zones WHERE id = $1::text::uuid",
            &[&zone_id],
        )
        .await?;

    let zone_status = if let Some(row) = zone_rows.first() {
        row.get::<_, String>(0)
    } else {
        "disabled".to_string()
    };

    // [COMMENT]: 2. Lấy trạng thái của toàn bộ Service từ bảng hierarchy.zone_services
    let svc_rows = pg_client
        .query(
            "SELECT service_type::text, desired_state FROM hierarchy.zone_services \
             WHERE zone_id = $1::text::uuid",
            &[&zone_id],
        )
        .await?;

    let mut services = std::collections::HashMap::new();
    for row in svc_rows {
        let svc_type: String = row.get(0);
        let enabled: bool = row.get(1);
        services.insert(svc_type, enabled);
    }

    Ok((zone_status, services))
}

/// Applies Mail and Storage observations in one round-trip. Disabled services
/// remain SRE-owned and are not mutated by telemetry.
pub async fn update_reported_service_health(
    pg_client: &tokio_postgres::Client,
    zone_id: &str,
    mail_status: &str,
    mail_enabled: bool,
    storage_status: &str,
    storage_enabled: bool,
    observed_at_unix_seconds: i64,
) -> Result<u64, Box<dyn std::error::Error>> {
    // [COMMENT]: Pure UPDATE chỉ ghi actual_state — KHÔNG dùng UPSERT để tránh trigger WAL event.
    // Lý do: UPSERT (INSERT ... ON CONFLICT DO UPDATE) gây ra WAL event chứa toàn bộ row bao gồm
    // desired_state. CDC Streamer đọc desired_state từ WAL → publish service_status_changed lên Redis
    // → Dataplane nhận và log spam mỗi 5 giây dù desired_state không thực sự thay đổi.
    //
    // [COMMENT]: Observation timestamp fence ngăn nhiều JO replica apply report cũ sau report mới.
    // Pure UPDATE chỉ ghi actual_state: nếu row không tồn tại (zone_services chưa được tạo) thì bỏ qua.
    // zone_services luôn được tạo sẵn khi khởi tạo zone → safe để dùng UPDATE-only.
    let rows_affected = pg_client
        .execute(
            "UPDATE hierarchy.zone_services AS service \
             SET actual_state = observation.status::hierarchy.zone_service_status, \
                 actual_observed_at = to_timestamp($6::bigint), \
                 updated_at = NOW() \
             FROM (VALUES \
                 ('mail'::text, $2::text, $3::bool), \
                 ('storage'::text, $4::text, $5::bool) \
             ) AS observation(service_type, status, enabled) \
             WHERE service.zone_id = $1::text::uuid \
               AND service.service_type::text = observation.service_type \
               AND observation.enabled \
               AND (service.actual_observed_at IS NULL \
                    OR service.actual_observed_at < to_timestamp($6::bigint))",
            &[
                &zone_id,
                &mail_status,
                &mail_enabled,
                &storage_status,
                &storage_enabled,
                &observed_at_unix_seconds,
            ],
        )
        .await?;

    Ok(rows_affected)
}

pub async fn update_loaded_payload_keys(
    pg_client: &tokio_postgres::Client,
    zone_id: &str,
    loaded_keys: &[super::proto::LoadedPayloadKey],
    observed_at_unix_seconds: i64,
    leader_fencing_token: i64,
) -> Result<u64, Box<dyn std::error::Error>> {
    let key_ids: Vec<String> = loaded_keys
        .iter()
        .filter_map(|key| uuid::Uuid::from_slice(&key.key_id).ok())
        .map(|key_id| key_id.to_string())
        .collect();
    let fingerprints: Vec<Vec<u8>> = loaded_keys
        .iter()
        .map(|key| key.public_key_fingerprint.clone())
        .collect();
    let rows_affected = pg_client
        .execute(
            "UPDATE hierarchy.zone_encryption_keys AS zone_key \
             SET loaded_at = CASE WHEN EXISTS ( \
                     SELECT 1 FROM unnest($2::text[], $3::bytea[]) AS loaded(key_id, fingerprint) \
                     WHERE loaded.key_id::uuid = zone_key.id \
                       AND loaded.fingerprint = zone_key.fingerprint \
                 ) THEN to_timestamp($4::bigint) ELSE NULL END, \
                 loaded_observed_at = to_timestamp($4::bigint), \
                 loaded_observed_fencing_token = $5::bigint, \
                 updated_at = NOW() \
             WHERE zone_key.zone_id = $1::text::uuid \
               AND (zone_key.loaded_observed_fencing_token IS NULL \
                    OR zone_key.loaded_observed_fencing_token < $5::bigint \
                    OR (zone_key.loaded_observed_fencing_token = $5::bigint \
                        AND (zone_key.loaded_observed_at IS NULL \
                             OR zone_key.loaded_observed_at <= to_timestamp($4::bigint))))",
            &[
                &zone_id,
                &key_ids,
                &fingerprints,
                &observed_at_unix_seconds,
                &leader_fencing_token,
            ],
        )
        .await?;
    Ok(rows_affected)
}

/// [COMMENT]: Bootstrap Snapshot — Lấy toàn bộ desired_state của tất cả zone_services từ DB.
/// Được gọi một lần khi JO khởi động để khởi tạo EnabledServicesMap in-memory trước khi
/// subscribe CDC. Đảm bảo không có khoảng trống về trạng thái enabled/disabled sau JO restart.
///
/// Return: HashMap<zone_id, HashMap<service_type, desired_state>>
pub async fn query_all_zone_services_enabled(
    pg_client: &tokio_postgres::Client,
) -> Result<
    std::collections::HashMap<String, std::collections::HashMap<String, bool>>,
    Box<dyn std::error::Error + Send + Sync>,
> {
    // [COMMENT]: Lấy toàn bộ zone_services — cast sang text để tránh enum mapping issue
    let rows = pg_client
        .query(
            "SELECT zone_id::text, service_type::text, desired_state \
             FROM hierarchy.zone_services",
            &[],
        )
        .await?;

    // [COMMENT]: Nhóm theo zone_id → {service_type: desired_state}
    let mut snapshot: std::collections::HashMap<String, std::collections::HashMap<String, bool>> =
        std::collections::HashMap::new();

    for row in rows {
        let zone_id: String = row.get(0);
        let service_type: String = row.get(1);
        let desired_state: bool = row.get(2);
        snapshot
            .entry(zone_id)
            .or_default()
            .insert(service_type, desired_state);
    }

    Logger::sys_info(
        "zone_db.bootstrap_snapshot",
        &format!(
            "Bootstrap zone_services snapshot thành công: {} zones loaded.",
            snapshot.len()
        ),
    );

    Ok(snapshot)
}
