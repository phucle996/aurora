use crate::observability::logger::Logger;
use tokio_postgres::NoTls;

/// [COMMENT]: Upsert một Hypervisor Node vào bảng `hypervisor.nodes` (Auto-Discovery + Heartbeat Update).
///
/// Luồng xử lý:
///   - INSERT nếu node_code chưa tồn tại trong zone (Auto-discovery: sinh UUID mới).
///   - UPDATE nếu đã tồn tại, áp dụng race-condition guard: chỉ ghi nếu `last_active_at < sent_at`.
///
/// Guard `last_active_at < sent_at` là bắt buộc theo SoT §5.4 để chống out-of-order heartbeats
/// khi nhiều Dataplane node gửi report đồng thời qua Kafka transport.
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

    // [COMMENT]: Sinh UUID dạng string để cast trong SQL (tương thích pattern hiện tại của db.rs).
    // Trên DB thực tế: ID chỉ được dùng khi INSERT (auto-discovery), UPDATE không đổi ID.
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
        // [COMMENT]: rows_affected = 0 có thể do: (1) guard last_active_at bị block (out-of-order),
        // hoặc (2) không có gì thay đổi. Cả hai đều là hành vi bình thường, không cần log error.
        Ok(false)
    }
}

/// [COMMENT]: Đánh dấu danh sách Hypervisor Nodes sang trạng thái `disconnected` trong PostgreSQL.
///
/// Được gọi bởi Dead Man's Switch node-level trong listener/deadman.rs:
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
