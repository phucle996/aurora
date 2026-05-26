use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use std::time::Duration;
use tokio::time::sleep;
use tokio_util::sync::CancellationToken;

use crate::job_receiver::message::JobPayload;
use crate::job_receiver::result::JobExecutionResult;
use crate::job_receiver::result::JobResultReporter;
use crate::job_receiver::admission::AdmissionController;
use crate::policyengine::engine::PolicyEngine;
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
///   - Tích hợp **Early Timeout (9/10 idle)** bảo vệ an toàn luồng, không tranh chấp khóa.
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
        policy_engine: Arc<PolicyEngine>,
        redis_job: Arc<RedisClientManager>,
        redis_internal_zone: Arc<RedisClientManager>,
        worker_id: usize,
        cancel_token: CancellationToken,
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
            // 1. Trích xuất giới hạn max_workers động từ Policy Engine cấu hình
            let max_workers = policy_engine
                .current()
                .policies
                .get("max_workers")
                .and_then(|v| v.as_u64())
                .unwrap_or(100) as usize;

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
                let idle_secs = payload.idle;

                // 7. Thiết lập khóa phân phối Lease Lock trên redis_internal_zone
                match crate::infra::redis::query::acquire_lease_lock(redis_internal_zone.client(), &lock_key, idle_secs).await {
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

                let active_jobs_clone = active_jobs.clone();
                let redis_job_clone = redis_job.clone();
                let redis_internal_zone_clone = redis_internal_zone.clone();
                let stream_key_clone = stream_key.clone();

                // 8. Spawn task xử lý độc lập bọc trong Early Timeout (9/10 idle)
                tokio::spawn(async move {
                    let timeout_duration = Duration::from_secs((idle_secs as u64 * 9) / 10);
                    
                    Logger::sys_info(
                        "job.ingestion",
                        &format!(
                            "Worker {} starting workload execution for job {} with Early Timeout: {:?}",
                            worker_id, payload.job_id, timeout_duration
                        )
                    );

                    match tokio::time::timeout(timeout_duration, Self::dispatch_workload(payload.clone())).await {
                        Ok(_) => {
                            // Xử lý thành công -> Commit: Gọi XACK giải phóng Job
                            Logger::job_log(
                                &payload.job_id,
                                &payload.job_topic,
                                payload.attempt,
                                "job.success",
                                "Workload executed successfully within timeout limits"
                            );

                            let ack_id = payload.redis_msg_id.as_deref().unwrap_or(&payload.job_id);
                            let _ = crate::infra::redis::query::acknowledge_message(redis_job_clone.client(), &stream_key_clone, "dataplane-group", ack_id)
                                .await;

                            // Báo cáo kết quả thành công lên Controlplane
                            let report = JobExecutionResult {
                                job_id: payload.job_id.clone(),
                                job_version: payload.job_version,
                                attempt: payload.attempt,
                                result_status: "SUCCEEDED".to_string(),
                                error_code: None,
                                message: "Workload completed successfully".to_string(),
                            };
                            let _ = JobResultReporter::report_via_grpc(&report).await;
                        }
                        Err(_) => {
                            // Lỗi Early Timeout -> Hủy bỏ cục bộ, không gọi XACK để nhường cho chu kỳ phục hồi
                            Logger::sys_error(
                                "job.ingestion",
                                &format!(
                                    "CRITICAL: Early Timeout expired for Job {} ({:?}). Local task terminated.",
                                    payload.job_id, timeout_duration
                                ),
                                "EARLY_TIMEOUT"
                            );

                            // Báo cáo lỗi thất bại lên Controlplane để cập nhật giao diện
                            let report = JobExecutionResult {
                                job_id: payload.job_id.clone(),
                                job_version: payload.job_version,
                                attempt: payload.attempt,
                                result_status: "FAILED".to_string(),
                                error_code: Some("EARLY_TIMEOUT".to_string()),
                                message: format!("Workload execution exceeded 9/10 limit of idle time ({:?})", timeout_duration),
                            };
                            let _ = JobResultReporter::report_via_grpc(&report).await;
                        }
                    }

                    // Giải phóng khóa Lease Lock trên redis_internal_zone
                    let _ = crate::infra::redis::query::release_lease_lock(redis_internal_zone_clone.client(), &lock_key).await;

                    // Giảm số lượng job đang xử lý khi hoàn tất
                    active_jobs_clone.fetch_sub(1, Ordering::SeqCst);
                });
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
