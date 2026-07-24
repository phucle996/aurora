use std::fs;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use tokio::time::sleep;

use crate::infra::kafka::KafkaTransport;
use crate::infra::zone_kv::ZoneKvStore;
use crate::observability::logger::Logger;
use crate::workerpool::lifecycle::WorkerLifecycleManager;

static CPU_USAGE_PCT: AtomicUsize = AtomicUsize::new(0);
static RAM_USAGE_PCT: AtomicUsize = AtomicUsize::new(0);

/// ============================================================================
/// 📂 MODULE: observability/resource.rs - Giám Sát & Báo Cáo Tài Nguyên Node
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Đọc trực tiếp `/proc/stat` và `/proc/meminfo` để đo hiệu năng CPU và RAM của Node.
///   - Đẩy các chỉ số tài nguyên cục bộ kèm số workers vào health bucket của Zone KV.
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

    /// Khởi chạy vòng lặp giám sát và ghi snapshot riêng cho node vào Zone KV.
    pub fn start_dataplane_resource_snapshot_writer(
        node_id: String,
        zone_kv: Arc<ZoneKvStore>,
        worker_pool: Arc<WorkerLifecycleManager>,
        kafka: Arc<KafkaTransport>,
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
                // [COMMENT]: Đây là cached lag của partition Kafka assign cho pod, không mở
                // broker polling riêng. Zone leader mới aggregate tất cả node snapshots.
                let (job_queue_lag, job_queue_lag_stale) = kafka.job_lag_snapshot();
                Logger::record_pipeline_metrics(&crate::config::Config::get_global().zone_id);

                let now = SystemTime::now()
                    .duration_since(UNIX_EPOCH)
                    .map(|duration| duration.as_secs())
                    .unwrap_or_default();
                let key = format!(
                    "zone.node.{}",
                    node_id.replace(
                        |character: char| !character.is_ascii_alphanumeric()
                            && character != '-'
                            && character != '_',
                        "_"
                    )
                );
                let snapshot = serde_json::json!({
                    "cpu": cpu_usage / 100.0,
                    "ram": ram_usage / 100.0,
                    "active_workers": active_workers,
                    "job_queue_lag": job_queue_lag,
                    "job_queue_lag_stale": job_queue_lag_stale,
                    "updated_at": now
                });
                match serde_json::to_vec(&snapshot) {
                    Ok(value) => match tokio::time::timeout(
                        Duration::from_secs(2),
                        zone_kv.health_put(&key, bytes::Bytes::from(value)),
                    )
                    .await
                    {
                        Ok(Ok(_)) => {}
                        Ok(Err(error)) => Logger::sys_warn_with_fields(
                            "resource.snapshot",
                            "DATAPLANE_RESOURCE_SNAPSHOT_WRITE_FAILED",
                            "Could not write local Dataplane resource snapshot to Zone health KV",
                            &error,
                            crate::observability::logger::LogFields {
                                operation_id: Some(&node_id),
                                retryable: Some(true),
                                outcome: Some("stale"),
                                ..Default::default()
                            },
                        ),
                        Err(_) => Logger::sys_warn_with_fields(
                            "resource.snapshot",
                            "DATAPLANE_RESOURCE_SNAPSHOT_WRITE_TIMEOUT",
                            "Timed out writing local Dataplane resource snapshot to Zone health KV",
                            "",
                            crate::observability::logger::LogFields {
                                operation_id: Some(&node_id),
                                retryable: Some(true),
                                outcome: Some("stale"),
                                ..Default::default()
                            },
                        ),
                    },
                    Err(error) => Logger::sys_error(
                        "resource.snapshot",
                        "Could not serialize local Dataplane resource snapshot",
                        &error.to_string(),
                    ),
                }

                // Thực hiện chu kỳ quét và đẩy tài nguyên định kỳ mỗi 5 giây (đồng bộ tải CPU/IO)
                sleep(Duration::from_secs(5)).await;
            }
        });
    }
}
