use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use std::time::Duration;
use tokio::time::sleep;
use tokio_util::sync::CancellationToken;

// Sử dụng các module message và admission từ thư mục job_lifecycle mới đổi tên
use crate::job_lifecycle::message::JobPayload;
use crate::job_lifecycle::admission::AdmissionController;
// Đã loại bỏ import PolicyEngine
use crate::infra::redis::RedisClientManager;
use crate::observability::logger::Logger;

/// ============================================================================
/// 📂 MODULE: job_receiver/consumer.rs - Trình Phân Phối Nghiệp Vụ Trung Tâm
/// ============================================================================
/// 
/// 📌 VAI TRÒ (ROLE):
///   - Đóng vai trò là Ingestion Engine chính của hệ thống.
///   - Gọi module tách biệt `AdmissionController` quản lý sức chứa và ngắt mạch (Circuit Breaker).
///   - Hiện thực hóa mô hình **Lease Lock (Distributed Transaction)** qua `redis_internal_zone`
///     để đảm bảo tính nguyên tử giao dịch, không bị mất mát Job khi crash node.
///   - Hỗ trợ **Graceful CancellationToken Drain** để đóng/thu nhỏ luồng an toàn khi Scale Down.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Trạng thái hàng đợi công việc nằm trực tiếp trên Redis Stream (`jobs:<zone_id>`).
///   - Cấu hình trần `max_workers` được đọc trực tiếp từ Policy Engine snapshot.
///
pub struct JobConsumer;

impl JobConsumer {
    /// Bắt đầu vòng lặp đọc dữ liệu bất đồng bộ từ Redis Stream (Ingestion loop).
    pub async fn start_ingestion(
        config: Arc<crate::config::Config>,
        redis_job: Arc<RedisClientManager>,
        redis_internal_zone: Arc<RedisClientManager>,
        worker_id: usize,
        cancel_token: CancellationToken,
        worker_pool: Arc<crate::workerpool::lifecycle::WorkerLifecycleManager>,
        active_lock_registry: Arc<crate::workerpool::watchdog::ActiveLockRegistry>,
    ) {
        let stream_key = format!("jobs:{}", config.zone_id);
        Logger::sys_info(
            "job.ingestion",
            &format!(
                "Starting Worker {} consumer loop with Admission Control & Distributed Lease Lock on stream '{}'...",
                worker_id, stream_key
            )
        );

        // Đếm số lượng Job đang xử lý đồng thời tại instance này
        let active_jobs = Arc::new(AtomicUsize::new(0));
        let mut admission_controller = AdmissionController::new();

        loop {
            // 1. Trích xuất giới hạn max_workers tĩnh trực tiếp từ Config (đã lược bỏ PolicyEngine)
            let max_workers = config.max_workers;

            // 2. Lấy số lượng job hiện hành và thực hiện tính toán qua AdmissionController
            let current_active = active_jobs.load(Ordering::SeqCst);
            let admission = admission_controller.evaluate(current_active, max_workers);

            // 3. Nếu mạch đang ngắt (quá tải), tạm dừng chu kỳ kéo và thăm dò lại sau 500ms
            if admission.is_broken {
                tokio::select! {
                    _ = cancel_token.cancelled() => {
                        Logger::sys_info(
                            "job.ingestion",
                            &format!("Worker {} Ingestion Loop cancelled (Circuit Broken state). Exiting gracefully...", worker_id)
                        );
                        break;
                    }
                    _ = sleep(Duration::from_millis(500)) => {}
                }
                continue;
            }

            // 4. Nhịp trễ kéo giãn động (Pull Pacing) do áp lực ngược
            if admission.pacing_delay_ms > 0 {
                tokio::select! {
                    _ = cancel_token.cancelled() => {
                        Logger::sys_info(
                            "job.ingestion",
                            &format!("Worker {} Ingestion Loop cancelled (Pacing Delay state). Exiting gracefully...", worker_id)
                        );
                        break;
                    }
                    _ = sleep(Duration::from_millis(admission.pacing_delay_ms)) => {}
                }
            }

            // 5. Mạch bình thường -> Gọi Redis Stream kéo job mới (Blocking Read bọc trong select)
            let fetch_fut = crate::infra::redis::query::fetch_next_stream_message(redis_job.client(), &stream_key);
            let raw_message = tokio::select! {
                _ = cancel_token.cancelled() => {
                    Logger::sys_info(
                        "job.ingestion",
                        &format!("Worker {} Ingestion Loop cancelled (Waiting for Message state). Exiting gracefully...", worker_id)
                    );
                    break;
                }
                res = fetch_fut => {
                    match res {
                        Ok(msg) => msg,
                        Err(e) => {
                            Logger::sys_error(
                                "job.ingestion",
                                &format!("Worker {} failed to fetch stream message: {}", worker_id, e),
                                "REDIS_FETCH_ERROR"
                            );
                            sleep(Duration::from_secs(2)).await;
                            continue;
                        }
                    }
                }
            };

            if raw_message == "{}" {
                // Không phát sinh message mới -> Thăm dò lại sau 1 giây
                tokio::select! {
                    _ = cancel_token.cancelled() => {
                        Logger::sys_info(
                            "job.ingestion",
                            &format!("Worker {} Ingestion Loop cancelled (Empty Message state). Exiting gracefully...", worker_id)
                        );
                        break;
                    }
                    _ = sleep(Duration::from_secs(1)) => {}
                }
                continue;
            }

            // 6. Giải mã gói tin
            if let Ok(payload) = serde_json::from_str::<JobPayload>(&raw_message) {
                // Ghi log Audit nhận Job ngay lập tức
                Logger::job_log(
                    &payload.job_id,
                    &payload.job_topic,
                    payload.attempt,
                    "job.received",
                    &format!("Worker {} successfully claimed raw job message from Redis Stream", worker_id)
                );

                let lock_key = format!("locks:job:{}", payload.job_id);

                // 7. Thiết lập khóa phân phối Lease Lock trên redis_internal_zone
                match crate::infra::redis::query::acquire_lease_lock(redis_internal_zone.client(), &lock_key).await {
                    Ok(acquired) => {
                        if !acquired {
                            Logger::sys_warn(
                                "job.ingestion",
                                &format!("Lock key '{}' already held by another instance. Skipping job ID: {}", lock_key, payload.job_id),
                                "LOCK_ACQUISITION_FAILED"
                            );
                            continue;
                        }
                    }
                    Err(e) => {
                        Logger::sys_error(
                            "job.ingestion",
                            &format!("Worker {} failed to connect to redis_internal_zone for locking job {}: {}", worker_id, payload.job_id, e),
                            "REDIS_LOCK_ERROR"
                        );
                        sleep(Duration::from_secs(1)).await;
                        continue;
                    }
                }

                // Tăng số lượng job đang xử lý
                active_jobs.fetch_add(1, Ordering::SeqCst);

                // Giao việc cho Orchestrated Runner thuộc module job_lifecycle mới đổi tên để xử lý chạy ngầm (non-blocking)
                crate::job_lifecycle::runner::JobRunner::run_job(
                    payload,
                    worker_pool.clone(),
                    redis_job.clone(),
                    redis_internal_zone.clone(),
                    active_lock_registry.clone(),
                    active_jobs.clone(),
                    stream_key.clone(),
                );
            }
        }
    }

    /// Định tuyến động nghiệp vụ (Dynamic Workload Routing) dựa vào Topic.
    pub async fn dispatch_workload(
        payload: JobPayload,
        worker_pool: Arc<crate::workerpool::lifecycle::WorkerLifecycleManager>,
    ) -> Result<crate::executor::ExecutionResult, crate::executor::ExecutorError> {
        Logger::sys_info(
            "job.ingestion",
            &format!(
                "Dispatching job {} (topic: {}) to dedicated executor...",
                payload.job_id, payload.job_topic
            ),
        );

        let topic = payload.job_topic.clone();
        let parts: Vec<&str> = topic.split('.').collect();
        if parts.len() != 2 {
            return Err(crate::executor::ExecutorError::ExecutionFailed(format!(
                "Invalid job topic format: {}",
                topic
            )));
        }

        let workload = parts[0];
        let action = parts[1];

        match workload {
            "mail" => {
                crate::executor::mail::dispatch_mail_job(action, payload, worker_pool).await
            }
            "vps" => {
                crate::executor::hypervisor::dispatch_vps_job(action, payload).await
            }
            _ => Err(crate::executor::ExecutorError::ExecutionFailed(format!(
                "Unsupported workload type: {}",
                workload
            ))),
        }
    }
}
