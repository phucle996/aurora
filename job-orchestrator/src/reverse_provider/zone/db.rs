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

/// [COMMENT]: Cập nhật trạng thái kích hoạt của Zone Service trực tiếp trong PostgreSQL.
/// Giao dịch có tính nguyên tử, sử dụng ON CONFLICT để UPSERT an toàn trong môi trường HA.
pub async fn update_zone_service_status(
    db_url: &str,
    zone_id: &str,
    service_type: &str,
    enabled: bool,
) -> Result<bool, Box<dyn std::error::Error>> {
    let (pg_client, connection) = tokio_postgres::connect(db_url, NoTls).await?;

    tokio::spawn(async move {
        if let Err(e) = connection.await {
            Logger::sys_error(
                "zone_db.connection_svc",
                "Lỗi kết nối chạy ngầm của PostgreSQL khi cập nhật Service",
                &e.to_string(),
            );
        }
    });

    // [COMMENT]: Sinh ID ngẫu nhiên dạng chuỗi để ép kiểu trong SQL (Bypass ToSql UUID missing feature)
    let svc_id_str = uuid::Uuid::new_v4().to_string();

    // [COMMENT]: Atomic UPSERT trên bảng hierarchy.zone_services.
    // Điều kiện WHERE trong DO UPDATE ngăn ghi nếu desired_state không đổi (No-op guard tối ưu IOPS).
    let rows_affected = pg_client
        .execute(
            "INSERT INTO hierarchy.zone_services (id, zone_id, service_type, desired_state, created_at, updated_at) \
             VALUES ($1::text::uuid, $2::text::uuid, $3::text::hierarchy.zone_service_type, $4, NOW(), NOW()) \
             ON CONFLICT (zone_id, service_type) \
             DO UPDATE SET desired_state = EXCLUDED.desired_state, updated_at = NOW() \
             WHERE zone_services.desired_state != EXCLUDED.desired_state",
            &[&svc_id_str, &zone_id, &service_type, &enabled],
        )
        .await?;

    if rows_affected > 0 {
        Logger::sys_info(
            "zone_db.update_service",
            &format!(
                "Đã cập nhật Service '{}' của Zone {} sang enabled: {} trực tiếp trong DB SoT.",
                service_type, zone_id, enabled
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

    let svc_id_str = uuid::Uuid::new_v4().to_string();

    // [COMMENT]: Atomic UPSERT trên bảng hierarchy.zone_services - chỉ ghi actual_state.
    // capacity không lưu DB (transient metric), chỉ dùng cho Decision Engine trong RAM.
    let rows_affected = pg_client
        .execute(
            "INSERT INTO hierarchy.zone_services \
             (id, zone_id, service_type, desired_state, actual_state, created_at, updated_at) \
             VALUES ($1::text::uuid, $2::text::uuid, $3::text::hierarchy.zone_service_type, \
                     false, $4::text::hierarchy.zone_service_status, NOW(), NOW()) \
             ON CONFLICT (zone_id, service_type) \
             DO UPDATE SET actual_state = EXCLUDED.actual_state, updated_at = NOW()",
            &[&svc_id_str, &zone_id, &service_type, &status],
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
