use crate::infra::redis::RedisClientManager;
use crate::job_receiver::consumer::JobConsumer;
use crate::job_receiver::message::JobPayload;
use crate::job_receiver::result::{JobExecutionResult, JobResultReporter};
use crate::observability::logger::Logger;
use crate::workerpool::lifecycle::WorkerLifecycleManager;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use std::time::Duration;

/// Guard tự động giải phóng khóa phân phối Lease Lock và giảm bộ đếm active_jobs
/// khi khối xử lý kết thúc (hoặc bị hủy/timeout/panic), ngăn chặn rò rỉ tài nguyên.
struct ExecutionCleanupGuard {
    redis_internal_zone: Arc<RedisClientManager>,
    lock_key: String,
    active_jobs: Arc<AtomicUsize>,
}

impl Drop for ExecutionCleanupGuard {
    fn drop(&mut self) {
        let redis = self.redis_internal_zone.clone();
        let lock = self.lock_key.clone();
        let active = self.active_jobs.clone();

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
        active_jobs: Arc<AtomicUsize>,
        stream_key: String,
    ) {
        let lock_key = format!("locks:job:{}", payload.job_id);
        let idle_opt = payload.idle;

        tokio::spawn(async move {
            let timeout_duration = match idle_opt {
                Some(secs) if secs > 0 => Duration::from_secs((secs as u64 * 9) / 10),
                _ => Duration::from_secs(3600 * 24 * 365), // 1 year (no limit)
            };
            let job_id = payload.job_id.clone();
            let result_channel = format!("job_results:{}", job_id);

            Logger::sys_info(
                "job.runner",
                &format!(
                    "Job Runner: Starting execution wrapper for job {} with Early Timeout: {:?}",
                    job_id, timeout_duration
                ),
            );

            // Đăng ký guard tự động dọn dẹp tài nguyên
            let _guard = ExecutionCleanupGuard {
                redis_internal_zone: redis_internal_zone.clone(),
                lock_key: lock_key.clone(),
                active_jobs: active_jobs.clone(),
            };

            // Thực thi định tuyến và gọi Executor nghiệp vụ, bọc trong Early Timeout
            let payload_dispatch = payload.clone();
            let worker_pool_dispatch = worker_pool.clone();
            let exec_res = tokio::time::timeout(
                timeout_duration,
                JobConsumer::dispatch_workload(payload_dispatch, worker_pool_dispatch),
            )
            .await;

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
    }
}
