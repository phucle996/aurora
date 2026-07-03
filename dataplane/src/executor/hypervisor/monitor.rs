use std::collections::HashMap;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use crate::config::Config;
use crate::infra::redis::RedisClientManager;
use crate::observability::logger::Logger;

/// ============================================================================
/// 📂 MODULE: executor/hypervisor/monitor.rs — Hypervisor Node Health Monitor
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Poll Proxmox Cluster API mỗi 15 giây để lấy danh sách node vật lý và metrics.
///   - Tính toán trạng thái (connected / degraded / disconnected) của từng node.
///   - Ghi kết quả vào Redis L2 `infra:hypervisor` hash để ZoneStatusGateway đọc và đẩy lên Platform.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - `hypervisor_connection_lifecycle_god_view.md` §Luồng B (Auto-Discovery & Heartbeat Flow)
///
/// 🔒 RANH GIỚI BẢO MẬT (PRIVACY BOUNDARY):
///   - PROXMOX_API_URL và PROXMOX_API_TOKEN CHỈ tồn tại trong env var của Dataplane process.
///   - TUYỆT ĐỐI không push credentials lên Controlplane DB hoặc Redis Platform L1.
///
pub struct HypervisorMonitor;

/// Cấu trúc node trả về từ Proxmox API `GET /api2/json/nodes`
/// Chỉ map những field cần thiết, bỏ qua phần còn lại (defensive deserialization)
#[derive(serde::Deserialize, Debug)]
struct ProxmoxNodeRaw {
    /// Định danh node vật lý trong Proxmox (ví dụ: "pve-node-01")
    node: String,
    /// Trạng thái của node theo Proxmox: "online" | "offline" | "unknown"
    status: String,
    /// Mức độ sử dụng CPU hiện tại (float 0.0–1.0, tỉ lệ trên maxcpu)
    #[serde(default)]
    cpu: f64,
    /// Tổng số lõi CPU vật lý
    #[serde(default)]
    maxcpu: u64,
    /// Bộ nhớ RAM đang sử dụng (bytes)
    #[serde(default)]
    mem: u64,
    /// Tổng dung lượng RAM vật lý (bytes)
    #[serde(default)]
    maxmem: u64,
    /// Dung lượng disk OS node đang dùng (bytes)
    #[serde(default)]
    disk: u64,
    /// Tổng dung lượng disk OS node (bytes)
    #[serde(default)]
    maxdisk: u64,
}

/// Wrapper lớp ngoài của Proxmox API response envelope
#[derive(serde::Deserialize)]
struct ProxmoxApiResponse {
    data: Vec<ProxmoxNodeRaw>,
}

/// Cấu trúc JSON ghi vào Redis L2 `infra:hypervisor` hash (per node_code field)
/// Phải sync với schema trong SoT §2.1 (Redis Key Patterns)
#[derive(serde::Serialize)]
struct HypervisorNodeCache {
    status: String,
    cpu_cores_total: u64,
    cpu_cores_used: u64,
    ram_mb_total: u64,
    ram_mb_used: u64,
    storage_gb_total: u64,
    storage_gb_used: u64,
    updated_at: u64,
}

impl HypervisorMonitor {
    /// Khởi chạy background task polling Proxmox Cluster API định kỳ 15 giây.
    /// Kết quả ghi vào Redis L2 `infra:hypervisor` hash để ZoneStatusGateway tổng hợp.
    pub fn start(config: Arc<Config>, redis_internal_zone: Arc<RedisClientManager>) {
        // Không khởi chạy nếu Proxmox chưa được cấu hình (Graceful Degradation)
        if config.proxmox_api_url.is_empty() || config.proxmox_api_token.is_empty() {
            Logger::sys_warn(
                "hypervisor_monitor.start",
                "PROXMOX_API_URL hoặc PROXMOX_API_TOKEN chưa được cấu hình. HypervisorMonitor sẽ không khởi chạy.",
                "Hypervisor workload sẽ không được báo cáo lên Platform.",
            );
            return;
        }

        tokio::spawn(async move {
            Logger::sys_info(
                "hypervisor_monitor.start",
                &format!(
                    "HypervisorMonitor: Bắt đầu polling Proxmox Cluster tại {} mỗi 15 giây...",
                    config.proxmox_api_url
                ),
            );

            // Xây dựng reqwest client một lần duy nhất, tái sử dụng toàn bộ vòng lặp
            // để tận dụng Connection Pool và tránh TCP churn mỗi 15 giây
            let client = Self::build_http_client(&config);

            // Maintain connection tới Redis L2 (tự động reconnect khi cần)
            let mut conn_opt = None;

            loop {
                // Đảm bảo có kết nối Redis L2 hợp lệ trước khi thực hiện ghi
                if conn_opt.is_none() {
                    match redis_internal_zone
                        .client()
                        .get_multiplexed_tokio_connection()
                        .await
                    {
                        Ok(conn) => conn_opt = Some(conn),
                        Err(e) => {
                            Logger::sys_error(
                                "hypervisor_monitor.redis_connect",
                                "Không thể kết nối Redis L2, thử lại sau 5 giây...",
                                &e.to_string(),
                            );
                            tokio::time::sleep(Duration::from_secs(5)).await;
                            continue;
                        }
                    }
                }

                let conn = conn_opt.as_mut().unwrap();

                // --- 1. Poll Proxmox API lấy danh sách node vật lý ---
                let poll_result = Self::fetch_proxmox_nodes(&client, &config).await;

                let now = SystemTime::now()
                    .duration_since(UNIX_EPOCH)
                    .unwrap_or_default()
                    .as_secs();

                match poll_result {
                    Ok(nodes) if !nodes.is_empty() => {
                        // --- 2. Với mỗi node, tính toán trạng thái và ghi vào Redis L2 ---
                        for node_raw in &nodes {
                            let status = Self::compute_node_status(node_raw);

                            // Chuyển đổi đơn vị: bytes -> MB (RAM), bytes -> GB (Storage)
                            let ram_mb_total = node_raw.maxmem / 1_048_576;
                            let ram_mb_used = node_raw.mem / 1_048_576;
                            let storage_gb_total = node_raw.maxdisk / 1_073_741_824;
                            let storage_gb_used = node_raw.disk / 1_073_741_824;

                            // Tính cpu_cores_used: round(cpu_ratio * maxcpu)
                            let cpu_cores_used =
                                (node_raw.cpu * node_raw.maxcpu as f64).round() as u64;

                            let cache_entry = HypervisorNodeCache {
                                status: status.clone(),
                                cpu_cores_total: node_raw.maxcpu,
                                cpu_cores_used,
                                ram_mb_total,
                                ram_mb_used,
                                storage_gb_total,
                                storage_gb_used,
                                updated_at: now,
                            };

                            // Serialize sang JSON, bỏ qua node nếu lỗi serialize
                            let json_val = match serde_json::to_string(&cache_entry) {
                                Ok(j) => j,
                                Err(e) => {
                                    Logger::sys_error(
                                        "hypervisor_monitor.serialize",
                                        &format!(
                                            "Không thể serialize cache entry cho node {}",
                                            node_raw.node
                                        ),
                                        &e.to_string(),
                                    );
                                    continue;
                                }
                            };

                            // [COMMENT]: HSET infra:hypervisor <node_code> <JSON>
                            // Thao tác ghi idempotent — ghi đè an toàn mỗi chu kỳ 15s
                            let write_res: Result<(), redis::RedisError> = redis::cmd("HSET")
                                .arg("infra:hypervisor")
                                .arg(&node_raw.node)
                                .arg(&json_val)
                                .query_async(conn)
                                .await;

                            if let Err(e) = write_res {
                                Logger::sys_error(
                                    "hypervisor_monitor.redis_write",
                                    &format!(
                                        "Lỗi ghi Redis L2 infra:hypervisor cho node {}",
                                        node_raw.node
                                    ),
                                    &e.to_string(),
                                );
                                // Reset connection để tự khôi phục vòng tiếp theo
                                conn_opt = None;
                                break;
                            }

                            Logger::sys_debug(
                                "hypervisor_monitor.update",
                                &format!(
                                    "Đã cập nhật node {} -> status={}, cpu={}/{}, ram={}MB/{}MB",
                                    node_raw.node,
                                    status,
                                    cpu_cores_used,
                                    node_raw.maxcpu,
                                    ram_mb_used,
                                    ram_mb_total
                                ),
                            );
                        }

                        Logger::sys_debug(
                            "hypervisor_monitor.poll_ok",
                            &format!(
                                "HypervisorMonitor: Đã cập nhật {} node vào infra:hypervisor.",
                                nodes.len()
                            ),
                        );
                    }

                    Ok(_) => {
                        // Proxmox trả về danh sách rỗng — không làm gì (giữ trạng thái cũ)
                        Logger::sys_warn(
                            "hypervisor_monitor.empty_response",
                            "Proxmox API trả về danh sách node rỗng.",
                            "Giữ nguyên trạng thái cache Redis cũ.",
                        );
                    }

                    Err(e) => {
                        // --- 3. Kết nối Proxmox thất bại -> mark tất cả node đã biết là disconnected ---
                        Logger::sys_error(
                            "hypervisor_monitor.poll_fail",
                            "Không thể kết nối Proxmox API. Đánh dấu toàn bộ node đã biết là disconnected.",
                            &e,
                        );

                        if let Some(c) = conn_opt.as_mut() {
                            Self::mark_all_known_nodes_disconnected(c, now).await;
                        }
                    }
                }

                // [COMMENT]: Chu kỳ 15 giây theo SoT §4 Luồng B
                tokio::time::sleep(Duration::from_secs(15)).await;
            }
        });
    }

    /// Xây dựng reqwest HTTP client với cấu hình TLS phù hợp.
    /// Client được tái sử dụng toàn vòng lặp (Connection Pool tránh TCP churn).
    fn build_http_client(config: &Config) -> reqwest::Client {
        let mut builder = reqwest::Client::builder().timeout(Duration::from_secs(5)); // Timeout cứng 5 giây để tránh treo vòng lặp

        // Chỉ cho phép skip TLS verify trên dev/test — production bắt buộc tắt
        if config.proxmox_tls_insecure {
            builder = builder.danger_accept_invalid_certs(true);
        }

        builder.build().unwrap_or_else(|e| {
            Logger::sys_error(
                "hypervisor_monitor.http_client",
                "Không thể khởi tạo HTTP client cho Proxmox. Sử dụng default client.",
                &e.to_string(),
            );
            reqwest::Client::new()
        })
    }

    /// Poll Proxmox REST API `/api2/json/nodes` để lấy danh sách node và metrics.
    /// Trả về `Err(String)` nếu kết nối thất bại hoặc response không parse được.
    async fn fetch_proxmox_nodes(
        client: &reqwest::Client,
        config: &Config,
    ) -> Result<Vec<ProxmoxNodeRaw>, String> {
        let url = format!(
            "{}/api2/json/nodes",
            config.proxmox_api_url.trim_end_matches('/')
        );

        let response = client
            .get(&url)
            // [COMMENT]: Proxmox yêu cầu Authorization header dạng: PVEAPIToken=user@realm!id=secret
            .header("Authorization", &config.proxmox_api_token)
            .send()
            .await
            .map_err(|e| e.to_string())?;

        if !response.status().is_success() {
            return Err(format!(
                "Proxmox API trả về HTTP {}: {}",
                response.status(),
                response.text().await.unwrap_or_default()
            ));
        }

        let api_resp: ProxmoxApiResponse = response
            .json()
            .await
            .map_err(|e| format!("Không thể parse Proxmox API response: {}", e))?;

        Ok(api_resp.data)
    }

    /// Tính toán trạng thái logic của node dựa trên metrics thực tế từ Proxmox.
    /// Logic State Machine theo SoT §3.2:
    ///   - Proxmox status != "online" -> disconnected
    ///   - CPU > 90% hoặc RAM > 90% -> degraded
    ///   - Bình thường -> connected
    fn compute_node_status(node: &ProxmoxNodeRaw) -> String {
        // Node offline trên Proxmox -> disconnected ngay
        if node.status != "online" {
            return "disconnected".to_string();
        }

        // Tính tỉ lệ RAM nếu maxmem > 0
        let ram_pct = if node.maxmem > 0 {
            (node.mem as f64 / node.maxmem as f64) * 100.0
        } else {
            0.0
        };

        // cpu là ratio 0.0-1.0 trên tổng maxcpu cores
        let cpu_pct = node.cpu * 100.0;

        // [COMMENT]: Ngưỡng DEGRADED theo SoT §3.2 State Machine: CPU/RAM > 90%
        if cpu_pct > 90.0 || ram_pct > 90.0 {
            return "degraded".to_string();
        }

        "connected".to_string()
    }

    /// Đánh dấu toàn bộ node đã biết trong Redis L2 `infra:hypervisor` sang trạng thái `disconnected`.
    /// Được gọi khi Proxmox API hoàn toàn không accessible (network failure, credential expired...).
    async fn mark_all_known_nodes_disconnected(
        conn: &mut redis::aio::MultiplexedConnection,
        now: u64,
    ) {
        // Lấy danh sách các node đang có trong cache
        let known: HashMap<String, String> = redis::cmd("HGETALL")
            .arg("infra:hypervisor")
            .query_async(conn)
            .await
            .unwrap_or_default();

        for (node_code, json_str) in &known {
            // Parse JSON cũ, chỉ đổi status -> disconnected, giữ nguyên metrics
            let mut node_val: serde_json::Value =
                serde_json::from_str(json_str).unwrap_or(serde_json::json!({}));

            node_val["status"] = serde_json::Value::String("disconnected".to_string());
            node_val["updated_at"] = serde_json::Value::Number(serde_json::Number::from(now));

            let updated_json = node_val.to_string();

            let _: Result<(), redis::RedisError> = redis::cmd("HSET")
                .arg("infra:hypervisor")
                .arg(node_code)
                .arg(&updated_json)
                .query_async(conn)
                .await;
        }

        if !known.is_empty() {
            Logger::sys_warn(
                "hypervisor_monitor.mark_disconnected",
                &format!(
                    "Đã đánh dấu {} node sang disconnected do Proxmox API không phản hồi.",
                    known.len()
                ),
                "Proxmox API Unavailable",
            );
        }
    }
}
