use crate::observability::logger::Logger;
use tokio_postgres::NoTls;

/// [COMMENT]: Cập nhật trạng thái Zone trực tiếp trong PostgreSQL (Direct DB Write - Bypass CP).
/// Áp dụng ràng buộc State Machine nghiêm ngặt để tránh cache RAM stale của JO ghi đè cấu hình thủ công SRE.
/// Trả về true nếu ghi thành công, false nếu DB Guard từ chối (chuyển trạng thái không hợp lệ).
pub async fn update_zone_status(
    db_url: &str,
    zone_id: &str,
    status: &str,
) -> Result<bool, Box<dyn std::error::Error>> {
    let (pg_client, connection) = tokio_postgres::connect(db_url, NoTls).await?;

    tokio::spawn(async move {
        if let Err(e) = connection.await {
            Logger::sys_error(
                "zone_db.connection",
                "Lỗi kết nối chạy ngầm của PostgreSQL",
                &e.to_string(),
            );
        }
    });

    // [COMMENT]: Thực thi lệnh UPDATE kèm ràng buộc State Machine (hierarchy.zone_status enum).
    // Chỉ cho phép chuyển giữa active/congested/draining với nhau, và chuyển sang disabled từ mọi trạng thái vận hành.
    // Các trạng thái SRE-owned (planned, maintenance) không được tự động chuyển.
    let rows_affected = pg_client
        .execute(
            "UPDATE hierarchy.zones \
             SET status = $1::text::hierarchy.zone_status, updated_at = NOW() \
             WHERE id = $2::text::uuid \
               AND status != $1::text::hierarchy.zone_status \
               AND ( \
                   ($1::text IN ('active', 'congested', 'draining') AND status::text IN ('active', 'congested', 'draining')) \
                   OR \
                   ($1::text = 'disabled' AND status::text IN ('active', 'congested', 'draining', 'maintenance')) \
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

/// [COMMENT]: Lấy trạng thái hiện tại của Zone và Service từ DB để đồng bộ cache lúc khởi chạy (Cold Start Sync).
/// Được gọi khi zone_heartbeats cache không có entry cho zone_id (lần đầu xuất hiện trong stream).
pub async fn query_current_state(
    db_url: &str,
    zone_id: &str,
    service_type: &str,
) -> Result<(String, bool), Box<dyn std::error::Error>> {
    let (pg_client, connection) = tokio_postgres::connect(db_url, NoTls).await?;

    tokio::spawn(async move {
        if let Err(e) = connection.await {
            Logger::sys_error(
                "zone_db.query_connection",
                "Lỗi DB connection",
                &e.to_string(),
            );
        }
    });

    // [COMMENT]: Truy vấn trạng thái Zone từ bảng hierarchy.zones.
    // Cast status sang text để driver deserialize được thành String không bị panic do thiếu mapper enum.
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

    // [COMMENT]: Truy vấn trạng thái Service từ bảng hierarchy.zone_services
    let svc_rows = pg_client
        .query(
            "SELECT desired_state FROM hierarchy.zone_services \
             WHERE zone_id = $1::text::uuid AND service_type = $2::text::hierarchy.zone_service_type",
            &[&zone_id, &service_type],
        )
        .await?;

    let svc_enabled = if let Some(row) = svc_rows.first() {
        row.get::<_, bool>(0)
    } else {
        false
    };

    Ok((zone_status, svc_enabled))
}

/// [COMMENT]: Truy vấn đầy đủ metadata của Zone (Status & trạng thái enabled của tất cả Service).
/// Được gọi bởi query.rs (PubSub metadata responder) để phản hồi Dataplane reconciliation request.
pub async fn query_zone_metadata(
    db_url: &str,
    zone_id: &str,
) -> Result<(String, std::collections::HashMap<String, bool>), Box<dyn std::error::Error>> {
    let (pg_client, connection) = tokio_postgres::connect(db_url, NoTls).await?;

    tokio::spawn(async move {
        if let Err(e) = connection.await {
            Logger::sys_error(
                "zone_db.query_metadata_connection",
                "Lỗi DB connection",
                &e.to_string(),
            );
        }
    });

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

/// [COMMENT]: Cập nhật trạng thái sức khỏe vận hành (actual_state) của một dịch vụ trong Postgres.
/// Được gọi với throttle logic trong processor.rs: chỉ ghi khi status đổi, capacity chênh >10, hoặc >120s.
pub async fn update_zone_service_metrics(
    db_url: &str,
    zone_id: &str,
    service_type: &str,
    status: &str,
    capacity: i32,
    observed_at_unix_seconds: i64,
) -> Result<bool, Box<dyn std::error::Error>> {
    let (pg_client, connection) = tokio_postgres::connect(db_url, NoTls).await?;

    tokio::spawn(async move {
        if let Err(e) = connection.await {
            Logger::sys_error(
                "zone_db.connection_svc_metrics",
                "Lỗi kết nối chạy ngầm của PostgreSQL khi cập nhật metrics Service",
                &e.to_string(),
            );
        }
    });

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
            "UPDATE hierarchy.zone_services \
             SET actual_state = $1::text::hierarchy.zone_service_status, \
                 actual_observed_at = to_timestamp($4), updated_at = NOW() \
             WHERE zone_id = $2::text::uuid \
               AND service_type = $3::text::hierarchy.zone_service_type \
               AND (actual_observed_at IS NULL OR actual_observed_at < to_timestamp($4))",
            &[&status, &zone_id, &service_type, &observed_at_unix_seconds],
        )
        .await?;

    if rows_affected > 0 {
        Logger::sys_info(
            "zone_db.update_service_metrics",
            &format!(
                "Đã cập nhật chỉ số Service '{}' của Zone {} (status: '{}', capacity: {}%) trực tiếp trong DB.",
                service_type, zone_id, status, capacity
            ),
        );
        Ok(true)
    } else {
        Ok(false)
    }
}

/// [COMMENT]: Bootstrap Snapshot — Lấy toàn bộ desired_state của tất cả zone_services từ DB.
/// Được gọi một lần khi JO khởi động để khởi tạo EnabledServicesMap in-memory trước khi
/// subscribe CDC. Đảm bảo không có khoảng trống về trạng thái enabled/disabled sau JO restart.
///
/// Return: HashMap<zone_id, HashMap<service_type, desired_state>>
pub async fn query_all_zone_services_enabled(
    db_url: &str,
) -> Result<
    std::collections::HashMap<String, std::collections::HashMap<String, bool>>,
    Box<dyn std::error::Error + Send + Sync>,
> {
    let (pg_client, connection) = tokio_postgres::connect(db_url, NoTls).await?;

    tokio::spawn(async move {
        if let Err(e) = connection.await {
            Logger::sys_error(
                "zone_db.bootstrap_connection",
                "Lỗi DB connection khi bootstrap zone_services snapshot",
                &e.to_string(),
            );
        }
    });

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

/// [COMMENT]: Lấy desired_state của tất cả service thuộc một zone cụ thể từ DB.
/// Được gọi làm fallback khi zone_heartbeats cache không có entry cho zone_id
/// (ví dụ zone mới được tạo sau khi JO đã boot xong).
pub async fn query_zone_services_enabled(
    db_url: &str,
    zone_id: &str,
) -> Result<std::collections::HashMap<String, bool>, Box<dyn std::error::Error + Send + Sync>> {
    let (pg_client, connection) = tokio_postgres::connect(db_url, NoTls).await?;

    tokio::spawn(async move {
        if let Err(e) = connection.await {
            Logger::sys_error(
                "zone_db.fallback_connection",
                "Lỗi DB connection khi fallback query zone_services",
                &e.to_string(),
            );
        }
    });

    let rows = pg_client
        .query(
            "SELECT service_type::text, desired_state \
             FROM hierarchy.zone_services \
             WHERE zone_id = $1::text::uuid",
            &[&zone_id],
        )
        .await?;

    let mut services = std::collections::HashMap::new();
    for row in rows {
        let svc_type: String = row.get(0);
        let enabled: bool = row.get(1);
        services.insert(svc_type, enabled);
    }

    Ok(services)
}
