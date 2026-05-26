use std::fs;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Duration;
use tokio::time::sleep;

static CPU_USAGE_PCT: AtomicUsize = AtomicUsize::new(0);
static RAM_USAGE_PCT: AtomicUsize = AtomicUsize::new(0);

/// ============================================================================
/// 📂 MODULE: observability/resource.rs - Giám Sát Tài Nguyên Hệ Thống Thô (Linux)
/// ============================================================================
/// 
/// 📌 VAI TRÒ (ROLE):
///   - Đọc trực tiếp `/proc/stat` và `/proc/meminfo` để đo hiệu năng CPU và RAM.
///   - Thu thập không đồng bộ qua luồng ngầm (non-blocking) để tránh nghẽn luồng hot-path.
///   - Cung cấp tốc độ đọc cực cao (<1ns) cho luồng kéo Job nhờ truy xuất qua Atomics.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Hệ thống file ảo `/proc` của hệ điều hành Linux (tương thích tuyệt đối K8s containers).
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

    /// Khởi chạy vòng lặp ngầm giám sát tài nguyên hệ thống (Linux /proc reader)
    pub fn start_monitor() {
        tokio::spawn(async {
            let mut prev_work = 0.0;
            let mut prev_total = 0.0;

            loop {
                // 1. Thu thập CPU từ /proc/stat
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
                                let cpu_usage = (diff_active / diff_total) * 100.0;
                                CPU_USAGE_PCT.store(cpu_usage as usize, Ordering::Relaxed);
                            }

                            prev_work = active;
                            prev_total = total;
                        }
                    }
                }

                // 2. Thu thập RAM từ /proc/meminfo
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
                        let ram_usage = (1.0 - (avail_mem / total_mem)) * 100.0;
                        RAM_USAGE_PCT.store(ram_usage as usize, Ordering::Relaxed);
                    }
                }

                sleep(Duration::from_secs(1)).await;
            }
        });
    }
}
