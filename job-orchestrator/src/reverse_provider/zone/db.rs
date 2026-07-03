use crate::observability::logger::Logger;
use tokio_postgres::NoTls;

/// Cập nhật trạng thái Zone trực tiếp trong PostgreSQL (Direct DB Write - Bypass CP)
/// Áp dụng cơ chế so sánh trạng thái để tránh ghi đè dư thừa (No-op check) nhằm tối ưu IOPS.
pub async fn update_zone_status(
    db_url: &str,
    zone_id: &str,
    status: &str,
) -> Result<bool, Box<dyn std::error::Error>> {
    // Thiết lập kết nối tạm thời tới Postgres DB SoT (Stateless connection)
    let (pg_client, connection) = tokio_postgres::connect(db_url, NoTls).await?;

    // Spawn luồng I/O chạy ngầm của postgres
    tokio::spawn(async move {
        if let Err(e) = connection.await {
            Logger::sys_error(
                "zone_db.connection",
                "Lỗi kết nối chạy ngầm của PostgreSQL",
                &e.to_string(),
            );
        }
    });

    // [COMMENT]: Thực thi lệnh UPDATE trên bảng hierarchy.zones, cast status sang text trước khi cast sang enum hierarchy.zone_status,
    // và cast zone_id từ text sang uuid để tránh lỗi serialization ở client-side.
    let rows_affected = pg_client
        .execute(
            "UPDATE hierarchy.zones SET status = $1::text::hierarchy.zone_status, updated_at = NOW() WHERE id = $2::text::uuid AND status != $1::text::hierarchy.zone_status",
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

/// Cập nhật trạng thái Zone Service trực tiếp trong PostgreSQL (Mail service status upsert)
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

    // Sinh ID ngẫu nhiên dạng chuỗi để ép kiểu trong SQL (Bypass ToSql UUID missing feature)
    let svc_id_str = uuid::Uuid::new_v4().to_string();

    // [COMMENT]: Thực thi câu lệnh UPSERT nguyên tử (Atomic upsert) trên bảng hierarchy.zone_services,
    // cast các uuid dạng string sang text trước khi cast sang uuid thực tế trong SQL, và cast service_type sang enum.
    let rows_affected = pg_client
        .execute(
            "INSERT INTO hierarchy.zone_services (id, zone_id, service_type, enabled, created_at, updated_at) \
             VALUES ($1::text::uuid, $2::text::uuid, $3::text::hierarchy.zone_service_type, $4, NOW(), NOW()) \
             ON CONFLICT (zone_id, service_type) \
             DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = NOW() \
             WHERE zone_services.enabled != EXCLUDED.enabled",
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

/// Lấy trạng thái hiện tại của Zone và Service từ DB để đồng bộ cache lúc khởi chạy
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
    // Cast status sang text để driver deserialize được thành String mà không bị panic do thiếu mapper enum.
    let zone_rows = pg_client
        .query(
            "SELECT status::text FROM hierarchy.zones WHERE id = $1::text::uuid",
            &[&zone_id],
        )
        .await?;

    let zone_status = if let Some(row) = zone_rows.first() {
        row.get::<_, String>(0)
    } else {
        "inactive".to_string()
    };

    // [COMMENT]: Truy vấn trạng thái Service từ bảng hierarchy.zone_services, cast zone_id và service_type
    let svc_rows = pg_client
        .query(
            "SELECT enabled FROM hierarchy.zone_services WHERE zone_id = $1::text::uuid AND service_type = $2::text::hierarchy.zone_service_type",
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

/// Truy vấn đầy đủ metadata của Zone (Status & trạng thái enabled của tất cả Service)
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
        "inactive".to_string()
    };

    // [COMMENT]: 2. Lấy trạng thái của toàn bộ Service từ bảng hierarchy.zone_services, cast zone_id
    let svc_rows = pg_client
        .query(
            "SELECT service_type::text, enabled FROM hierarchy.zone_services WHERE zone_id = $1::text::uuid",
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

/// Cập nhật trạng thái sức khỏe vận hành và năng lực xử lý thực tế của một dịch vụ trực tiếp trong Postgres
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

    // [COMMENT]: Thực thi câu lệnh UPSERT nguyên tử (Atomic upsert) trên bảng hierarchy.zone_services.
    // Sắp xếp các tham số tăng dần ($1 đến $5) khớp 100% với mảng Rust để Postgres suy luận kiểu chính xác.
    // Cast các tham số dạng String/&str sang text trước khi cast sang uuid/enum (cho cả service_type và status).
    let rows_affected = pg_client
        .execute(
            "INSERT INTO hierarchy.zone_services (id, zone_id, service_type, enabled, status, capacity, last_heartbeat_at, created_at, updated_at) \
             VALUES ($1::text::uuid, $2::text::uuid, $3::text::hierarchy.zone_service_type, false, $4::text::hierarchy.zone_service_status, $5, NOW(), NOW(), NOW()) \
             ON CONFLICT (zone_id, service_type) \
             DO UPDATE SET status = EXCLUDED.status, capacity = EXCLUDED.capacity, last_heartbeat_at = EXCLUDED.last_heartbeat_at, updated_at = NOW()",
            &[&svc_id_str, &zone_id, &service_type, &status, &capacity],
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
