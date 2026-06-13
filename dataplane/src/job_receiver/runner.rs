use crate::infra::redis::RedisClientManager;
use crate::job_receiver::consumer::JobConsumer;
use crate::job_receiver::message::JobPayload;
use crate::job_receiver::result::{JobExecutionResult, JobResultReporter};
use crate::observability::logger::Logger;
use crate::workerpool::lifecycle::WorkerLifecycleManager;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use std::time::Duration;
use tokio::time::Instant;

/// Guard tự động giải phóng khóa phân phối Lease Lock và giảm bộ đếm active_jobs
/// khi khối xử lý kết thúc (hoặc bị hủy/timeout/panic), ngăn chặn rò rỉ tài nguyên.
struct ExecutionCleanupGuard {
    redis_internal_zone: Arc<RedisClientManager>,
    lock_key: String,
    active_jobs: Arc<AtomicUsize>,
    active_lock_registry: Arc<crate::workerpool::watchdog::ActiveLockRegistry>,
}

impl Drop for ExecutionCleanupGuard {
    fn drop(&mut self) {
        let redis = self.redis_internal_zone.clone();
        let lock = self.lock_key.clone();
        let active = self.active_jobs.clone();
        let registry = self.active_lock_registry.clone();

        // Đồng bộ xóa khỏi Registry ngay lập tức để chặn chu kỳ watchdog tiếp theo gia hạn khóa
        registry.deregister(&lock);

        // Spawn một task độc lập để giải phóng tài nguyên bất đồng bộ ngoài tầm của task bị hủy
        tokio::spawn(async move {
            let _ = crate::infra::redis::query::release_lease_lock(redis.client(), &lock).await;
            active.fetch_sub(1, Ordering::SeqCst);
        });
    }
}

pub struct JobRunner;

impl JobRunner {
    /// Phân phối chạy ngầm và quản lý trọn vẹn vòng đời thực thi của một Job
    pub fn run_job(
        payload: JobPayload,
        worker_pool: Arc<WorkerLifecycleManager>,
        redis_job: Arc<RedisClientManager>,
        redis_internal_zone: Arc<RedisClientManager>,
        active_lock_registry: Arc<crate::workerpool::watchdog::ActiveLockRegistry>,
        active_jobs: Arc<AtomicUsize>,
        stream_key: String,
    ) {
        let lock_key = format!("locks:job:{}", payload.job_id);
        
        // Hạn mức thực thi tối đa lấy từ idle (mặc định 10 phút nếu không chỉ định)
        let limit = payload.idle
            .map(|s| Duration::from_secs(s as u64))
            .unwrap_or_else(|| Duration::from_secs(600));

        let lock_key_clone = lock_key.clone();
        let active_lock_registry_clone = active_lock_registry.clone();
        let job_id_clone = payload.job_id.clone();
        let job_version = payload.job_version;
        let attempt = payload.attempt;

        let (tx, rx) = tokio::sync::oneshot::channel();

        let handle = tokio::spawn(async move {
            // Chờ tín hiệu đăng ký hoàn tất vào Watchdog Registry để tránh race condition deregister trước register
            let _ = rx.await;

            let job_id = payload.job_id.clone();
            let result_channel = format!("job_results:{}", job_id);

            Logger::sys_info(
                "job.runner",
                &format!(
                    "Job Runner: Starting execution wrapper for job {} with Max Execution Limit: {:?}",
                    job_id, limit
                ),
            );

            // Đăng ký guard tự động dọn dẹp tài nguyên
            let _guard = ExecutionCleanupGuard {
                redis_internal_zone: redis_internal_zone.clone(),
                lock_key: lock_key.clone(),
                active_jobs: active_jobs.clone(),
                active_lock_registry: active_lock_registry_clone.clone(),
            };

            // Báo cáo trạng thái bắt đầu xử lý (PROCESSING) qua Redis Pub/Sub và gRPC
            let processing_report = JobExecutionResult {
                job_id: job_id.clone(),
                job_version: payload.job_version,
                attempt: payload.attempt,
                result_status: "PROCESSING".to_string(),
                error_code: None,
                message: "Job execution started on dataplane worker".to_string(),
            };
            let _ = JobResultReporter::report_outcome(
                redis_job.client(),
                &result_channel,
                &processing_report,
            )
            .await;

            // Thực thi định tuyến và gọi Executor nghiệp vụ, được giám sát bởi Watchdog bên ngoài
            let payload_dispatch = payload.clone();
            let worker_pool_dispatch = worker_pool.clone();
            let exec_res = Ok(JobConsumer::dispatch_workload(payload_dispatch, worker_pool_dispatch).await);

            // Phân loại kết quả thực thi
            let report = JobExecutionResult::from_outcome(
                job_id.clone(),
                payload.job_version,
                payload.attempt,
                exec_res,
            );

            // Ghi audit log kết quả thực thi
            if report.result_status == "SUCCEEDED" {
                Logger::job_log(
                    &payload.job_id,
                    &payload.job_topic,
                    payload.attempt,
                    "job.success",
                    &report.message,
                );
            } else {
                Logger::sys_error(
                    "job.runner",
                    &format!("Workload execution failed for job {}: {}", job_id, report.message),
                    report.error_code.as_deref().unwrap_or("UNKNOWN"),
                );
            }

            // Báo cáo kết quả đồng thời qua Redis Pub/Sub và gRPC
            let _ = JobResultReporter::report_outcome(
                redis_job.client(),
                &result_channel,
                &report,
            )
            .await;

            // Xác nhận giải phóng tin nhắn (XACK) trên Stream nếu xử lý thành công
            if report.result_status == "SUCCEEDED" {
                let ack_id = payload.redis_msg_id.as_deref().unwrap_or(&payload.job_id);
                let _ = crate::infra::redis::query::acknowledge_message(
                    redis_job.client(),
                    &stream_key,
                    "dataplane-group",
                    ack_id,
                )
                .await;
            }
        });

        // Đăng ký tác vụ và AbortHandle vào Watchdog để giám sát vòng đời
        active_lock_registry.register(
            lock_key_clone,
            Instant::now(),
            limit,
            handle.abort_handle(),
            job_id_clone,
            job_version,
            attempt,
        );

        // Kích hoạt tác vụ chạy sau khi đã đăng ký thành công
        let _ = tx.send(());
    }
}
