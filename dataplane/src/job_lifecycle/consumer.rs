use sha2::{Digest, Sha256};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use std::time::Duration;
use std::{hash::Hash, hash::Hasher};
use tokio::time::sleep;
use tokio_util::sync::CancellationToken;

// Sử dụng các module message và admission từ thư mục job_lifecycle mới đổi tên
use crate::job_lifecycle::admission::AdmissionController;
use crate::job_lifecycle::message::JobPayload;
// Đã loại bỏ import PolicyEngine
use crate::infra::redis::RedisClientManager;
use crate::infra::zone_kv::ZoneKvStore;
use crate::observability::logger::Logger;

/// ============================================================================
/// 📂 MODULE: job_receiver/consumer.rs - Trình Phân Phối Nghiệp Vụ Trung Tâm
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Đóng vai trò là Ingestion Engine chính của hệ thống.
///   - Gọi module tách biệt `AdmissionController` quản lý sức chứa và ngắt mạch (Circuit Breaker).
///   - Hiện thực hóa fenced lease qua NATS JetStream KV coordination bucket
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
    /// Phiên bản này hoạt động dưới dạng IngestionDaemon chạy xuyên suốt vòng đời app (Producer).
    // [COMMENT]: Nhận stream_key và group_name động từ tham số đầu vào để chạy dual loops
    #[allow(clippy::too_many_arguments)] // [COMMENT]: Hai ingestion loop truyền dependency rõ ràng, không gom vào context ẩn.
    pub async fn start_ingestion(
        config: Arc<crate::config::Config>,
        redis_job: Arc<RedisClientManager>,
        zone_kv: Arc<ZoneKvStore>,
        tx: tokio::sync::mpsc::Sender<JobPayload>,
        cancel_token: CancellationToken,
        active_jobs: Arc<AtomicUsize>,
        stream_key: String,
        group_name: String,
    ) {
        Logger::sys_info(
            "job.ingestion",
            &format!(
                "Starting persistent IngestionDaemon consumer loop with Admission Control & Distributed Lease Lock on stream '{}' in group '{}'...",
                stream_key, group_name
            )
        );

        let mut admission_controller = AdmissionController::new();
        let mut last_logged_status = String::new();

        loop {
            // [COMMENT]: Metadata là durable KV snapshot; lỗi đọc phải giữ last-safe default thay vì gọi Redis nội bộ.
            let zone_status = match zone_kv.read_zone_metadata().await {
                Ok(metadata) => metadata.status,
                Err(error) => {
                    if last_logged_status != "kv_unavailable" {
                        Logger::sys_error(
                            "job.ingestion",
                            "Không thể đọc Zone metadata; ingestion tạm dừng theo fail-closed",
                            &error,
                        );
                    }
                    "kv_unavailable".to_string()
                }
            };

            // Nếu trạng thái Zone không phải ACTIVE (planned, disabled, maintenance, draining) -> Tạm dừng kéo Job
            if zone_status != "active" {
                if zone_status != last_logged_status {
                    Logger::sys_info(
                        "job.ingestion",
                        &format!(
                            "Tạm dừng kéo job mới từ Platform L1 vì Zone ở trạng thái: '{}'.",
                            zone_status
                        ),
                    );
                    last_logged_status = zone_status.clone();
                }

                tokio::select! {
                    _ = cancel_token.cancelled() => {
                        Logger::sys_info(
                            "job.ingestion",
                            "IngestionDaemon Ingestion Loop cancelled (Zone Paused state). Exiting gracefully..."
                        );
                        break;
                    }
                    _ = sleep(Duration::from_secs(1)) => {}
                }
                continue;
            } else if !last_logged_status.is_empty() {
                Logger::sys_info(
                    "job.ingestion",
                    "Zone đã quay lại trạng thái ACTIVE. Tiếp tục kéo job mới.",
                );
                last_logged_status.clear();
            }

            // 1. Trích xuất giới hạn max_workers tĩnh trực tiếp từ Config
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
                            "IngestionDaemon Ingestion Loop cancelled (Circuit Broken state). Exiting gracefully..."
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
                            "IngestionDaemon Ingestion Loop cancelled (Pacing Delay state). Exiting gracefully..."
                        );
                        break;
                    }
                    _ = sleep(Duration::from_millis(admission.pacing_delay_ms)) => {}
                }
            }

            // 5. Mạch bình thường -> Gọi Redis Stream kéo job mới (Blocking Read bọc trong select)
            let fetch_fut = crate::infra::redis::query::fetch_next_stream_message(
                redis_job.client(),
                &stream_key,
                &group_name,
            );
            let opt_payload = tokio::select! {
                _ = cancel_token.cancelled() => {
                    Logger::sys_info(
                        "job.ingestion",
                        "IngestionDaemon Ingestion Loop cancelled (Waiting for Message state). Exiting gracefully..."
                    );
                    break;
                }
                res = fetch_fut => {
                    match res {
                        Ok(payload) => payload,
                        Err(e) => {
                            Logger::sys_error(
                                "job.ingestion",
                                &format!("IngestionDaemon failed to fetch stream message: {}", e),
                                "REDIS_FETCH_ERROR"
                            );
                            sleep(Duration::from_secs(2)).await;
                            continue;
                        }
                    }
                }
            };

            // 6. Kiểm tra xem có nhận được JobPayload hợp lệ hay không
            let mut payload = match opt_payload {
                Some(p) => p,
                None => {
                    // Không phát sinh message mới -> Thăm dò lại sau 1 giây
                    tokio::select! {
                        _ = cancel_token.cancelled() => {
                            Logger::sys_info(
                                "job.ingestion",
                                "IngestionDaemon Ingestion Loop cancelled (Empty Message state). Exiting gracefully..."
                            );
                            break;
                        }
                        _ = sleep(Duration::from_secs(1)) => {}
                    }
                    continue;
                }
            };

            // Ghi log Audit nhận Job ngay lập tức
            Logger::job_log(
                &payload.job_id,
                &payload.job_topic,
                payload.attempt,
                "job.received",
                "IngestionDaemon successfully claimed raw job message from Redis Stream",
            );

            if matches!(
                payload.job_topic.as_str(),
                "mail.consumer.upsert"
                    | "mail.consumer.delete"
                    | "mail.template.version_published"
                    | "mail.template.deleted"
            ) {
                // [COMMENT]: Stagger duplicate projection entries trước lock; mỗi node chỉ attempt đúng một lần.
                let mut hasher = std::collections::hash_map::DefaultHasher::new();
                std::env::var("HOSTNAME")
                    .unwrap_or_else(|_| std::process::id().to_string())
                    .hash(&mut hasher);
                payload.job_id.hash(&mut hasher);
                let jitter_ms = hasher.finish() % 250;
                tokio::time::sleep(Duration::from_millis(jitter_ms)).await;
            }

            // [COMMENT]: Digest giữ identity ổn định nhưng luôn thuộc grammar key của NATS KV, kể cả job_id từ domain ngoài.
            let job_key_digest = Sha256::digest(payload.job_id.as_bytes());
            let lock_key = format!("lease.job.{job_key_digest:x}");
            let owner_id = format!(
                "{}-{}",
                std::env::var("HOSTNAME").unwrap_or_else(|_| std::process::id().to_string()),
                uuid::Uuid::new_v4()
            );

            // [COMMENT]: NATS KV CAS cấp fencing token; job cũ không thể renew/release lease của owner mới.
            match zone_kv
                .acquire_lease(&lock_key, &owner_id, Duration::from_secs(30))
                .await
            {
                Ok(Some(lease)) => payload.zone_lease = Some(lease),
                Ok(None) => {
                    Logger::sys_warn(
                        "job.ingestion",
                        &format!(
                            "Lock key '{}' already held by another instance. Skipping job ID: {}",
                            lock_key, payload.job_id
                        ),
                        "LOCK_ACQUISITION_FAILED",
                    );
                    continue;
                }
                Err(e) => {
                    Logger::sys_error(
                        "job.ingestion",
                        &format!(
                            "IngestionDaemon failed to acquire Zone KV lease for job {}: {}",
                            payload.job_id, e
                        ),
                        "ZONE_KV_LOCK_ERROR",
                    );
                    sleep(Duration::from_secs(1)).await;
                    continue;
                }
            }

            // Tăng số lượng job đang xử lý (kể cả job đang chờ trong channel)
            active_jobs.fetch_add(1, Ordering::SeqCst);

            // Gửi payload vào channel cho Worker xử lý. Hỗ trợ cơ chế backpressure và cancel-safe khi dừng app.
            let acquired_lease = payload.zone_lease.clone();
            let send_fut = tx.send(payload);
            let send_res = tokio::select! {
                _ = cancel_token.cancelled() => {
                    Err("Shutdown initiated while sending to channel")
                }
                res = send_fut => {
                    res.map_err(|_| "Channel closed")
                }
            };

            if let Err(err_msg) = send_res {
                Logger::sys_error(
                    "job.ingestion",
                    &format!("Failed to dispatch job: {}. Releasing lock.", err_msg),
                    "CHANNEL_DISPATCH_ERROR",
                );
                active_jobs.fetch_sub(1, Ordering::SeqCst);
                if let Some(lease) = acquired_lease {
                    let _ = zone_kv.release_lease(&lease).await;
                }
            }
        }
    }

    /// Định tuyến động nghiệp vụ (Dynamic Workload Routing) dựa vào Topic.
    pub async fn dispatch_workload(
        payload: JobPayload,
        worker_pool: Arc<crate::workerpool::lifecycle::WorkerLifecycleManager>,
        redis_job: Arc<crate::infra::redis::RedisClientManager>,
        zone_id: &str,
    ) -> Result<crate::executor::ExecutionResult, crate::executor::ExecutorError> {
        Logger::sys_info(
            "job.ingestion",
            &format!(
                "Dispatching job {} (topic: {}) to dedicated executor...",
                payload.job_id, payload.job_topic
            ),
        );

        let topic = payload.job_topic.clone();
        let first_dot = match topic.find('.') {
            Some(idx) => idx,
            None => {
                return Err(crate::executor::ExecutorError::ExecutionFailed(format!(
                    "Invalid job topic format: {}",
                    topic
                )));
            }
        };
        let (workload, rest) = topic.split_at(first_dot);
        let action = &rest[1..];

        match workload {
            "mail" => {
                crate::executor::mail::dispatch_mail_job(
                    action,
                    payload,
                    worker_pool,
                    redis_job,
                    zone_id,
                )
                .await
            }
            "vps" => crate::executor::hypervisor::dispatch_vps_job(action, payload).await,
            "storage" => crate::executor::storage::dispatch_storage_job(action, payload).await,
            _ => Err(crate::executor::ExecutorError::ExecutionFailed(format!(
                "Unsupported workload type: {}",
                workload
            ))),
        }
    }
}
