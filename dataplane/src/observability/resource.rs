use std::fs;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use tokio::time::sleep;

use crate::infra::redis::RedisClientManager;
use crate::observability::logger::Logger;
use crate::workerpool::lifecycle::WorkerLifecycleManager;

static CPU_USAGE_PCT: AtomicUsize = AtomicUsize::new(0);
static RAM_USAGE_PCT: AtomicUsize = AtomicUsize::new(0);

/// ============================================================================
/// 📂 MODULE: observability/resource.rs - Giám Sát & Báo Cáo Tài Nguyên Node L2
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Đọc trực tiếp `/proc/stat` và `/proc/meminfo` để đo hiệu năng CPU và RAM của Node.
///   - Đẩy trực tiếp các chỉ số tài nguyên cục bộ này kèm số workers lên Redis L2 (Node Reporter).
///   - Cung cấp tốc độ đọc cực cao (<1ns) cho luồng nội bộ nhờ lưu trữ song song qua Atomics.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Hệ thống file ảo `/proc` của hệ điều hành Linux.
///
pub struct ResourceMonitor;

impl ResourceMonitor {
    /// Lấy chỉ số sử dụng CPU hiện tại dạng tỉ lệ (0.0 -> 1.0)
    pub fn cpu_usage() -> f64 {
        CPU_USAGE_PCT.load(Ordering::Relaxed) as f64 / 100.0
    }

    /// Lấy chỉ số sử dụng RAM hiện tại dạng tỉ lệ (0.0 -> 1.0)
    pub fn ram_usage() -> f64 {
        RAM_USAGE_PCT.load(Ordering::Relaxed) as f64 / 100.0
    }

    /// Khởi chạy vòng lặp ngầm giám sát tài nguyên hệ thống và tự động đẩy lên Redis L2 (Self-Healing Node Reporter)
    pub fn start_monitor(
        node_id: String,
        redis_internal_zone: Arc<RedisClientManager>,
        worker_pool: Arc<WorkerLifecycleManager>,
    ) {
        tokio::spawn(async move {
            Logger::sys_info(
                "resource_monitor.start",
                &format!(
                    "Bắt đầu ResourceMonitor & Node Reporter cho Node ID: {}...",
                    node_id
                ),
            );

            let mut prev_work = 0.0;
            let mut prev_total = 0.0;

            // Duy trì kết nối Multiplexed động tới Redis L2
            let mut conn_opt: Option<redis::aio::MultiplexedConnection> = None;

            loop {
                // 1. Thu thập CPU từ /proc/stat
                let mut cpu_usage = 0.0;
                if let Ok(stat) = fs::read_to_string("/proc/stat") {
                    if let Some(first_line) = stat.lines().next() {
                        let parts: Vec<&str> = first_line.split_whitespace().collect();
                        if parts.len() >= 5 && parts[0] == "cpu" {
                            let user: f64 = parts[1].parse().unwrap_or(0.0);
                            let nice: f64 = parts[2].parse().unwrap_or(0.0);
                            let system: f64 = parts[3].parse().unwrap_or(0.0);
                            let idle: f64 = parts[4].parse().unwrap_or(0.0);
                            let iowait: f64 = parts[5].parse().unwrap_or(0.0);
                            let irq: f64 = parts[6].parse().unwrap_or(0.0);
                            let softirq: f64 = parts[7].parse().unwrap_or(0.0);

                            let active = user + nice + system + irq + softirq;
                            let total = active + idle + iowait;

                            let diff_active = active - prev_work;
                            let diff_total = total - prev_total;

                            if diff_total > 0.0 {
                                cpu_usage = (diff_active / diff_total) * 100.0;
                                CPU_USAGE_PCT.store(cpu_usage as usize, Ordering::Relaxed);
                            }

                            prev_work = active;
                            prev_total = total;
                        }
                    }
                }

                // 2. Thu thập RAM từ /proc/meminfo
                let mut ram_usage = 0.0;
                if let Ok(meminfo) = fs::read_to_string("/proc/meminfo") {
                    let mut total_mem = 0.0;
                    let mut avail_mem = 0.0;
                    for line in meminfo.lines() {
                        if line.starts_with("MemTotal:") {
                            let parts: Vec<&str> = line.split_whitespace().collect();
                            if parts.len() >= 2 {
                                total_mem = parts[1].parse::<f64>().unwrap_or(0.0);
                            }
                        } else if line.starts_with("MemAvailable:") {
                            let parts: Vec<&str> = line.split_whitespace().collect();
                            if parts.len() >= 2 {
                                avail_mem = parts[1].parse::<f64>().unwrap_or(0.0);
                            }
                        }
                    }

                    if total_mem > 0.0 {
                        ram_usage = (1.0 - (avail_mem / total_mem)) * 100.0;
                        RAM_USAGE_PCT.store(ram_usage as usize, Ordering::Relaxed);
                    }
                }

                // 3. Đọc số lượng worker đang hoạt động trên Node hiện tại
                let active_workers = worker_pool.active_worker_ids().len();

                // 4. Kết nối và ghi nhận metrics của Node lên Redis L2 (Node Reporter)
                if conn_opt.is_none() {
                    if let Ok(conn) = redis_internal_zone
                        .client()
                        .get_multiplexed_tokio_connection()
                        .await
                    {
                        conn_opt = Some(conn);
                    }
                }

                if let Some(mut conn) = conn_opt.clone() {
                    let now = match SystemTime::now().duration_since(UNIX_EPOCH) {
                        Ok(dur) => dur.as_secs(),
                        Err(_) => 0,
                    };

                    let key = format!("dataplane:node:{}", node_id);

                    // Sử dụng pipeline HSET và EXPIRE bọc trong timeout 2s để tránh treo kết nối (HA & Anti-hang)
                    let redis_write_res = tokio::time::timeout(
                        Duration::from_secs(2),
                        redis::pipe()
                            .cmd("HSET")
                            .arg(&key)
                            .arg("cpu")
                            .arg(cpu_usage / 100.0) // Chuyển đổi về tỷ lệ 0.0 -> 1.0
                            .arg("ram")
                            .arg(ram_usage / 100.0) // Chuyển đổi về tỷ lệ 0.0 -> 1.0
                            .arg("active_workers")
                            .arg(active_workers)
                            .arg("updated_at")
                            .arg(now)
                            .cmd("EXPIRE")
                            .arg(&key)
                            .arg(15) // TTL 15s để tự dọn khi node crash hoặc scale down
                            .query_async::<_, ()>(&mut conn),
                    )
                    .await;

                    match redis_write_res {
                        Ok(Ok(())) => {
                            // Ghi thành công
                        }
                        _ => {
                            // Gặp lỗi ghi hoặc timeout -> Reset connection để tái khởi tạo ở chu kỳ sau
                            conn_opt = None;
                        }
                    }
                }

                // Thực hiện chu kỳ quét và đẩy tài nguyên định kỳ mỗi 5 giây (đồng bộ tải CPU/IO)
                sleep(Duration::from_secs(5)).await;
            }
        });
    }
}
