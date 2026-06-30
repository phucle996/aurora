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

    // Thực thi lệnh UPDATE, ép kiểu zone_id sang uuid để khớp schema
    // Chỉ update nếu status thực sự thay đổi (Optimize database writes)
    let rows_affected = pg_client
        .execute(
            "UPDATE core.zones SET status = $1, updated_at = NOW() WHERE id = $2::uuid AND status != $1",
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

    // Thực thi câu lệnh UPSERT nguyên tử (Atomic upsert)
    // Chỉ cập nhật updated_at nếu trạng thái enabled thực tế thay đổi
    let rows_affected = pg_client
        .execute(
            "INSERT INTO core.zone_services (id, zone_id, service_type, enabled, created_at, updated_at) \
             VALUES ($1::uuid, $2::uuid, $3, $4, NOW(), NOW()) \
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

    // Truy vấn trạng thái Zone
    let zone_rows = pg_client
        .query(
            "SELECT status FROM core.zones WHERE id = $1::uuid",
            &[&zone_id],
        )
        .await?;

    let zone_status = if let Some(row) = zone_rows.first() {
        row.get::<_, String>(0)
    } else {
        "inactive".to_string()
    };

    // Truy vấn trạng thái Service
    let svc_rows = pg_client
        .query(
            "SELECT enabled FROM core.zone_services WHERE zone_id = $1::uuid AND service_type = $2",
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

    // 1. Lấy trạng thái Zone
    let zone_rows = pg_client
        .query(
            "SELECT status FROM core.zones WHERE id = $1::uuid",
            &[&zone_id],
        )
        .await?;

    let zone_status = if let Some(row) = zone_rows.first() {
        row.get::<_, String>(0)
    } else {
        "inactive".to_string()
    };

    // 2. Lấy trạng thái của toàn bộ Service
    let svc_rows = pg_client
        .query(
            "SELECT service_type::text, enabled FROM core.zone_services WHERE zone_id = $1::uuid",
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
