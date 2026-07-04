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
            "SELECT desired_state FROM hierarchy.zone_services WHERE zone_id = $1::text::uuid AND service_type = $2::text::hierarchy.zone_service_type",
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
            "SELECT service_type::text, desired_state FROM hierarchy.zone_services WHERE zone_id = $1::text::uuid",
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
    // Loại bỏ hoàn toàn capacity và last_heartbeat_at khỏi DB, chỉ cập nhật actual_state (trước đây là status)
    let rows_affected = pg_client
        .execute(
            "INSERT INTO hierarchy.zone_services (id, zone_id, service_type, desired_state, actual_state, created_at, updated_at) \
             VALUES ($1::text::uuid, $2::text::uuid, $3::text::hierarchy.zone_service_type, false, $4::text::hierarchy.zone_service_status, NOW(), NOW()) \
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

/// Upsert một Hypervisor Node vào bảng `hypervisor.nodes` (Auto-Discovery + Heartbeat Update).
///
/// Luồng xử lý:
///   - INSERT nếu node_code chưa tồn tại trong zone (Auto-discovery: sinh UUID mới).
///   - UPDATE nếu đã tồn tại, áp dụng race-condition guard: chỉ ghi nếu `last_active_at < sent_at`.
///
/// Guard `last_active_at < sent_at` là bắt buộc theo SoT §5.4 để chống out-of-order heartbeats
/// khi nhiều Dataplane node gửi report đồng thời lên cùng một Platform Redis L1.
pub async fn upsert_hypervisor_node(
    db_url: &str,
    zone_id: &str,
    node_code: &str,
    status: &str,
    cpu_cores_total: i64,
    cpu_cores_used: i64,
    ram_mb_total: i64,
    ram_mb_used: i64,
    storage_gb_total: i64,
    storage_gb_used: i64,
    sent_at: i64, // Unix timestamp của heartbeat (phục vụ race-condition guard)
) -> Result<bool, Box<dyn std::error::Error>> {
    let (pg_client, connection) = tokio_postgres::connect(db_url, NoTls).await?;

    tokio::spawn(async move {
        if let Err(e) = connection.await {
            Logger::sys_error(
                "hypervisor_db.connection",
                "Lỗi kết nối chạy ngầm của PostgreSQL khi upsert hypervisor node",
                &e.to_string(),
            );
        }
    });

    // Sinh UUID dạng string để cast trong SQL (tương thích pattern hiện tại của db.rs)
    // Trên DB thực tế: ID chỉ được dùng khi INSERT (auto-discovery), UPDATE không đổi ID
    let new_id = uuid::Uuid::new_v4().to_string();

    // [COMMENT]: Câu lệnh UPSERT nguyên tử (Atomic Upsert) với ON CONFLICT (zone_id, node_code).
    // Mệnh đề WHERE trong DO UPDATE là race-condition guard: chỉ ghi khi data mới hơn data cũ.
    // Điều này đảm bảo out-of-order heartbeat (do network jitter) không ghi đè data mới hơn.
    // `name` được set bằng node_code khi auto-discovery (SRE chỉ read-only, không edit từ CP).
    let rows_affected = pg_client
        .execute(
            "INSERT INTO hypervisor.nodes \
             (id, zone_id, node_code, name, status, \
              cpu_cores_total, cpu_cores_used, ram_mb_total, ram_mb_used, \
              storage_gb_total, storage_gb_used, last_active_at, created_at, updated_at) \
             VALUES ($1::text::uuid, $2::text::uuid, $3, $3, $4, $5, $6, $7, $8, $9, $10, \
                     to_timestamp($11), NOW(), NOW()) \
             ON CONFLICT (zone_id, node_code) DO UPDATE \
             SET status               = EXCLUDED.status, \
                 cpu_cores_total      = EXCLUDED.cpu_cores_total, \
                 cpu_cores_used       = EXCLUDED.cpu_cores_used, \
                 ram_mb_total         = EXCLUDED.ram_mb_total, \
                 ram_mb_used          = EXCLUDED.ram_mb_used, \
                 storage_gb_total     = EXCLUDED.storage_gb_total, \
                 storage_gb_used      = EXCLUDED.storage_gb_used, \
                 last_active_at       = EXCLUDED.last_active_at, \
                 updated_at           = NOW() \
             WHERE hypervisor.nodes.last_active_at < EXCLUDED.last_active_at",
            &[
                &new_id,
                &zone_id,
                &node_code,
                &status,
                &cpu_cores_total,
                &cpu_cores_used,
                &ram_mb_total,
                &ram_mb_used,
                &storage_gb_total,
                &storage_gb_used,
                &sent_at,
            ],
        )
        .await?;

    if rows_affected > 0 {
        Logger::sys_info(
            "hypervisor_db.upsert_node",
            &format!(
                "Đã upsert Hypervisor Node '{}' của Zone {} (status: {}).",
                node_code, zone_id, status
            ),
        );
        Ok(true)
    } else {
        // rows_affected = 0 có thể do: (1) guard last_active_at bị block (out-of-order),
        // hoặc (2) không có gì thay đổi. Cả hai đều là hành vi bình thường.
        Ok(false)
    }
}

/// Đánh dấu danh sách Hypervisor Nodes sang trạng thái `disconnected` trong PostgreSQL.
///
/// Được gọi bởi Dead Man's Switch node-level trong listener.rs:
/// Nếu một node_code không xuất hiện trong report của zone quá 45 giây -> mark disconnected.
///
/// Dùng `ANY($2::text[])` để batch update nhiều node trong 1 round-trip DB (tối ưu IOPS).
pub async fn mark_hypervisor_nodes_disconnected(
    db_url: &str,
    zone_id: &str,
    node_codes: &[String],
) -> Result<u64, Box<dyn std::error::Error>> {
    if node_codes.is_empty() {
        return Ok(0);
    }

    let (pg_client, connection) = tokio_postgres::connect(db_url, NoTls).await?;

    tokio::spawn(async move {
        if let Err(e) = connection.await {
            Logger::sys_error(
                "hypervisor_db.connection_disconnect",
                "Lỗi kết nối chạy ngầm của PostgreSQL khi mark disconnected",
                &e.to_string(),
            );
        }
    });

    // [COMMENT]: Batch UPDATE: đánh dấu toàn bộ node trong danh sách sang disconnected.
    // Điều kiện AND status != 'disconnected' để tránh write vô nghĩa (No-op guard tối ưu IOPS).
    let rows_affected = pg_client
        .execute(
            "UPDATE hypervisor.nodes \
             SET status = 'disconnected', updated_at = NOW() \
             WHERE zone_id = $1::text::uuid \
               AND node_code = ANY($2) \
               AND status != 'disconnected'",
            &[&zone_id, &node_codes],
        )
        .await?;

    if rows_affected > 0 {
        Logger::sys_warn(
            "hypervisor_db.mark_disconnected",
            &format!(
                "Dead Man's Switch: Đã mark {} node của Zone {} sang disconnected (vắng mặt >45s).",
                rows_affected, zone_id
            ),
            "Node Heartbeat Timeout",
        );
    }

    Ok(rows_affected)
}
