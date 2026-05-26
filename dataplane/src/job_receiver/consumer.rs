use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use std::time::Duration;
use tokio::time::sleep;

use crate::job_receiver::message::JobPayload;
use crate::policyengine::engine::PolicyEngine;
use crate::workerpool::lifecycle::WorkerLifecycleManager;
use crate::infra::redis::RedisClientManager;
use crate::observability::logger::Logger;
use crate::observability::resource::ResourceMonitor;

/// ============================================================================
/// 📂 MODULE: job_receiver/consumer.rs - Trình Phân Phối Nghiệp Vụ Trung Tâm
/// ============================================================================
/// 
/// 📌 VAI TRÒ (ROLE):
///   - Đóng vai trò là Ingestion Engine chính của hệ thống.
///   - Tích hợp bộ **Admission Control / Circuit Breaker (Hysteresis)** chống quá tải:
///     * Trọng số kéo tin (Pull Weight $W$) giảm dần tuyến tính từ $1.0$ (khi $R = 0\%$) về $0.0$ (khi $R = 80\%$).
///     * $R = \max(\text{Active Job Ratio}, \text{CPU Usage}, \text{RAM Usage})$.
///     * Khi $R \ge 80\%$, mạch ngắt chuyển sang **`OPEN`**, ngưng nhận thêm Job mới hoàn toàn.
///     * Khi tải giảm xuống ngưỡng an toàn $R \le 50\%$, mạch ngắt tự động đóng lại **`CLOSED`** và phục hồi kéo Job.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Trạng thái hàng đợi công việc nằm trực tiếp trên Redis Stream (`jobs:<zone_id>`).
///   - Cấu hình trần `max_workers` được đọc trực tiếp từ Policy Engine snapshot.
///
pub struct JobConsumer;

impl JobConsumer {
    /// Bắt đầu vòng lặp đọc dữ liệu bất đồng bộ từ Redis Stream (Ingestion loop).
    pub async fn start_ingestion(
        policy_engine: Arc<PolicyEngine>,
        _worker_pool: Arc<WorkerLifecycleManager>,
        redis_job: Arc<RedisClientManager>,
    ) {
        Logger::sys_info("job.ingestion", "Starting stream consumer group processors with Admission Control & Dynamic Pull Pacing...");

        // Đếm số lượng Job đang xử lý đồng thời tại instance này
        let active_jobs = Arc::new(AtomicUsize::new(0));
        let mut is_circuit_broken = false;
        let base_delay = 1000.0; // Base delay: 1000ms

        loop {
            // 1. Trích xuất giới hạn max_workers động từ Policy Engine cấu hình
            let max_workers = policy_engine
                .current()
                .policies
                .get("max_workers")
                .and_then(|v| v.as_u64())
                .unwrap_or(100) as usize;

            // 2. Thu thập chỉ số tài nguyên tổng hợp
            let current_active = active_jobs.load(Ordering::SeqCst);
            let active_ratio = if max_workers > 0 {
                current_active as f64 / max_workers as f64
            } else {
                0.0
            };

            let cpu_usage = ResourceMonitor::cpu_usage();
            let ram_usage = ResourceMonitor::ram_usage();

            // R là giá trị lớn nhất trong ba chỉ số tài nguyên
            let r = active_ratio.max(cpu_usage).max(ram_usage);

            // 3. Tính toán Trọng số kéo tin (Pull Weight W) tuyến tính
            let w = if r < 0.8 {
                1.0 - (r / 0.8)
            } else {
                0.0
            };

            // 4. Tính toán nhịp trễ kéo giãn (Pacing Delay)
            let pacing_delay = base_delay * (1.0 - w);

            // 5. Kiểm thử điều kiện ngắt mạch (Circuit Breaker Hysteresis Check)
            if !is_circuit_broken && r >= 0.8 {
                is_circuit_broken = true;
                Logger::sys_warn(
                    "job.admission_control",
                    &format!(
                        "CRITICAL: Local instance resource load is too high ({:.1}% - active: {}/{}, CPU: {:.1}%, RAM: {:.1}%). Circuit Breaker OPEN: Pausing job ingestion loop...",
                        r * 100.0,
                        current_active,
                        max_workers,
                        cpu_usage * 100.0,
                        ram_usage * 100.0
                    ),
                    "High Load Circuit Breaker OPEN"
                );
            } else if is_circuit_broken && r <= 0.5 {
                is_circuit_broken = false;
                Logger::sys_info(
                    "job.admission_control",
                    &format!(
                        "RECOVERY: Resource load successfully recovered below safe threshold ({:.1}% - active: {}/{}, CPU: {:.1}%, RAM: {:.1}%). Circuit Breaker CLOSED: Resuming job ingestion loop...",
                        r * 100.0,
                        current_active,
                        max_workers,
                        cpu_usage * 100.0,
                        ram_usage * 100.0
                    )
                );
            }

            // 6. Nếu mạch đang ngắt (quá tải), tạm dừng chu kỳ kéo và thăm dò lại sau 500ms
            if is_circuit_broken {
                sleep(Duration::from_millis(500)).await;
                continue;
            }

            // 7. Nhịp trễ kéo giãn động (Pull Pacing) do áp lực ngược
            if pacing_delay > 0.0 {
                sleep(Duration::from_millis(pacing_delay as u64)).await;
            }

            // 8. Mạch bình thường -> Gọi Redis Stream kéo job mới (Blocking Read)
            if let Ok(raw_message) = redis_job.fetch_next_stream_message("jobs:zone-asia-southeast").await {
                if raw_message == "{}" {
                    // Không phát sinh message mới -> Thăm dò lại sau 1 giây
                    sleep(Duration::from_secs(1)).await;
                    continue;
                }

                // 9. Giải mã gói tin
                if let Ok(payload) = serde_json::from_str::<JobPayload>(&raw_message) {
                    // Tăng số lượng job đang xử lý
                    active_jobs.fetch_add(1, Ordering::SeqCst);

                    let active_jobs_clone = active_jobs.clone();
                    let redis_job_clone = redis_job.clone();

                    // 10. Spawn task xử lý độc lập
                    tokio::spawn(async move {
                        // Xác thực tính duy nhất và xử lý
                        Self::dispatch_workload(payload.clone()).await;

                        // Acknowledge hoàn thành xử lý gói tin lên Redis Stream
                        let _ = redis_job_clone.acknowledge_message("jobs:zone-asia-southeast", "dataplane-group", &payload.job_id).await;

                        // Giảm số lượng job đang xử lý khi hoàn tất
                        active_jobs_clone.fetch_sub(1, Ordering::SeqCst);
                    });
                }
            } else {
                // Lỗi kết nối Redis -> Sleep để tránh tạo spin-lock quá nhanh
                sleep(Duration::from_secs(2)).await;
            }
        }
    }

    /// Định tuyến động nghiệp vụ (Dynamic Workload Routing) dựa vào Topic.
    pub async fn dispatch_workload(payload: JobPayload) {
        Logger::sys_info(
            "job.ingestion",
            &format!("Dispatching job {} (topic: {}) to dedicated executor...", payload.job_id, payload.job_topic)
        );
        
        match payload.job_topic.as_str() {
            "vps.create" | "vps.resize" => {
                // Khởi gọi xử lý Hypervisor VPS
                sleep(Duration::from_millis(200)).await; // Giả lập thời gian chạy tác vụ
            }
            "mail.send" => {
                // Khởi gọi xử lý Mail
                sleep(Duration::from_millis(100)).await; // Giả lập thời gian chạy tác vụ
            }
            _ => {
                Logger::sys_error(
                    "job.ingestion",
                    &format!("Unsupported job topic received: {}", payload.job_topic),
                    "DISPATCH_ERROR"
                );
            }
        }
    }
}
