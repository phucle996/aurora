/// [COMMENT]: Upsert một Hypervisor Node vào bảng `hypervisor.nodes` (Auto-Discovery + Heartbeat Update).
///
/// Luồng xử lý:
///   - INSERT nếu node_code chưa tồn tại trong zone (Auto-discovery: sinh UUID mới).
///   - UPDATE nếu đã tồn tại, áp dụng race-condition guard: chỉ ghi nếu `last_active_at < sent_at`.
///
/// Guard `last_active_at < sent_at` là bắt buộc theo SoT §5.4 để chống out-of-order heartbeats
/// khi nhiều Dataplane node gửi report đồng thời qua Kafka transport.
pub struct HypervisorObservation<'a> {
    pub node_code: &'a str,
    pub status: &'a str,
    pub cpu_cores_total: i64,
    pub cpu_cores_used: i64,
    pub ram_mb_total: i64,
    pub ram_mb_used: i64,
    pub storage_gb_total: i64,
    pub storage_gb_used: i64,
    pub observed_at: i64,
}

pub async fn upsert_hypervisor_node(
    pg_client: &tokio_postgres::Client,
    zone_id: &str,
    observation: &HypervisorObservation<'_>,
) -> Result<bool, Box<dyn std::error::Error>> {
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
                     to_timestamp($11::bigint), NOW(), NOW()) \
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
                &observation.node_code,
                &observation.status,
                &observation.cpu_cores_total,
                &observation.cpu_cores_used,
                &observation.ram_mb_total,
                &observation.ram_mb_used,
                &observation.storage_gb_total,
                &observation.storage_gb_used,
                &observation.observed_at,
            ],
        )
        .await?;

    // rows_affected=0 is an expected out-of-order/no-op observation. Avoid
    // success logs on every heartbeat; OTel metrics own the hot-path signal.
    Ok(rows_affected > 0)
}
