use crate::infra::redis::RedisClientManager;
// Sử dụng các module nội bộ từ job_lifecycle mới đổi tên
use crate::job_lifecycle::consumer::JobConsumer;
use crate::job_lifecycle::message::JobPayload;
use crate::job_lifecycle::result::{JobExecutionResult, JobResultReporter};
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
        let job_topic_for_registry = payload.job_topic.clone();
        let trace_id_for_registry = payload.trace_id.clone();

        let (tx, rx) = tokio::sync::oneshot::channel();

        let handle = tokio::spawn(async move {
            // Chờ tín hiệu đăng ký hoàn tất vào Watchdog Registry để tránh race condition deregister trước register
            let _ = rx.await;

            let trace_id = payload.trace_id.clone();
            let stream_key_clone = stream_key.clone();

            crate::observability::otel::CURRENT_TRACE_ID.scope(trace_id, async move {
                use opentelemetry::trace::Span;
                use opentelemetry::trace::TraceContextExt;
                use opentelemetry::trace::Tracer;
                let tracer = opentelemetry::global::tracer("dataplane");

                let cx = if let Some(parent_ctx) = crate::observability::otel::OtelTracer::parse_traceparent(&payload.trace_id) {
                    opentelemetry::Context::current().with_remote_span_context(parent_ctx)
                } else {
                    opentelemetry::Context::current()
                };

                let mut span = tracer.start_with_context(format!("job.{}", payload.job_topic), &cx);

                let job_id = payload.job_id.clone();

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

                // Báo cáo trạng thái bắt đầu xử lý (PROCESSING) qua Redis Stream
                let processing_report = JobExecutionResult {
                    job_id: job_id.clone(),
                    job_version: payload.job_version,
                    attempt: payload.attempt,
                    result_status: "PROCESSING".to_string(),
                    error_code: None,
                    message: "Job execution started on dataplane worker".to_string(),
                    job_topic: payload.job_topic.clone(),
                    trace_id: payload.trace_id.clone(),
                };
                let _ = JobResultReporter::report_outcome(
                    redis_job.client(),
                    &processing_report,
                )
                .await;

                // Đo lường thời gian bắt đầu thực thi nghiệp vụ thô
                let start_time = tokio::time::Instant::now();

                // Thực thi định tuyến và gọi Executor nghiệp vụ, được giám sát bởi Watchdog bên ngoài
                let payload_dispatch = payload.clone();
                let worker_pool_dispatch = worker_pool.clone();
                let exec_res = Ok(JobConsumer::dispatch_workload(payload_dispatch, worker_pool_dispatch).await);

                let duration_ms = start_time.elapsed().as_secs_f64() * 1000.0;
                let zone_id = stream_key_clone.strip_prefix("jobs:").unwrap_or(&stream_key_clone).to_string();

                // Phân loại kết quả thực thi
                let report = JobExecutionResult::from_outcome(
                    job_id.clone(),
                    payload.job_version,
                    payload.attempt,
                    payload.job_topic.clone(),
                    payload.trace_id.clone(),
                    exec_res,
                );

                // Thiết lập trạng thái OTel span dựa theo kết quả của job
                if report.result_status == "SUCCEEDED" {
                    span.set_status(opentelemetry::trace::Status::Ok);
                } else {
                    span.set_status(opentelemetry::trace::Status::error(report.message.clone()));
                }
                span.end();

                // Ghi nhận OpenTelemetry metric đo độ trễ xử lý nghiệp vụ thực tế và tổng số lượng job
                crate::workerpool::metrics::WorkerMetricsManager::record_metrics(
                    crate::workerpool::metrics::MetricsType::HandlerLatencyMs {
                        zone_id,
                        job_topic: payload.job_topic.clone(),
                        status: report.result_status.clone(),
                        latency_ms: duration_ms,
                    }
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

                // Báo cáo kết quả qua Redis Stream (durable) để Job-Proxy cập nhật outbox table
                let _ = JobResultReporter::report_outcome(
                    redis_job.client(),
                    &report,
                )
                .await;

                // Xác nhận giải phóng tin nhắn (XACK) trên Stream khi hoàn tất xử lý (SUCCEEDED hoặc FAILED)
                if report.result_status == "SUCCEEDED" || report.result_status == "FAILED" {
                    let ack_id = payload.redis_msg_id.as_deref().unwrap_or(&payload.job_id);
                    let _ = crate::infra::redis::query::acknowledge_message(
                        redis_job.client(),
                        &stream_key_clone,
                        "dataplane-group",
                        ack_id,
                    )
                    .await;
                }
            }).await;
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
            job_topic_for_registry,
            trace_id_for_registry,
        );

        // Kích hoạt tác vụ chạy sau khi đã đăng ký thành công
        let _ = tx.send(());
    }
}
