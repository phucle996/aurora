use crate::infra::redis::RedisClientManager;
use crate::infra::zone_kv::{ZoneKvStore, ZoneLease};
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
    zone_kv: Arc<ZoneKvStore>,
    lease: ZoneLease,
    active_jobs: Arc<AtomicUsize>,
    active_lock_registry: Arc<crate::workerpool::watchdog::ActiveLockRegistry>,
}

impl Drop for ExecutionCleanupGuard {
    fn drop(&mut self) {
        let zone_kv = self.zone_kv.clone();
        let lease = self.lease.clone();
        let lock = lease.key.clone();
        let active = self.active_jobs.clone();
        let registry = self.active_lock_registry.clone();

        // Đồng bộ xóa khỏi Registry ngay lập tức để chặn chu kỳ watchdog tiếp theo gia hạn khóa
        registry.deregister(&lock);
        // [COMMENT]: Local admission không phụ thuộc network; NATS chậm/mất kết nối không được giữ sai active counter.
        active.fetch_sub(1, Ordering::SeqCst);

        // Spawn một task độc lập để giải phóng tài nguyên bất đồng bộ ngoài tầm của task bị hủy
        tokio::spawn(async move {
            let _ = zone_kv.release_lease(&lease).await;
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
        zone_kv: Arc<ZoneKvStore>,
        active_lock_registry: Arc<crate::workerpool::watchdog::ActiveLockRegistry>,
        active_jobs: Arc<AtomicUsize>,
        stream_key: String,
    ) {
        let Some(zone_lease) = payload.zone_lease.clone() else {
            Logger::sys_error(
                "job.runner",
                "Job reached runner without a Zone KV lease",
                "ZONE_LEASE_REQUIRED",
            );
            active_jobs.fetch_sub(1, Ordering::SeqCst);
            return;
        };
        let lock_key = zone_lease.key.clone();

        // Hạn mức thực thi tối đa lấy từ idle (mặc định 10 phút nếu không chỉ định)
        let limit = payload
            .idle
            .map(|s| Duration::from_secs(s as u64))
            .unwrap_or_else(|| Duration::from_secs(600));

        let lock_key_clone = lock_key.clone();
        let active_lock_registry_clone = active_lock_registry.clone();
        let job_id_clone = payload.job_id.clone();
        let job_version = payload.job_version;
        let attempt = payload.attempt;
        let job_topic_for_registry = payload.job_topic.clone();
        let source_domain_for_registry = payload.source_domain.clone();
        let trace_id_for_registry = payload.trace_id.clone();
        let zone_lease_for_task = zone_lease.clone();

        let (tx, rx) = tokio::sync::oneshot::channel();

        // [COMMENT]: Job task không thuộc worker receive-loop; guard riêng giữ nó trong graceful-shutdown barrier.
        let task_guard = worker_pool.track_task();

        let handle = tokio::spawn(async move {
            let _task_guard = task_guard;
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
                    zone_kv: zone_kv.clone(),
                    lease: zone_lease_for_task.clone(),
                    active_jobs: active_jobs.clone(),
                    active_lock_registry: active_lock_registry_clone.clone(),
                };

                if payload.reconcile_generation.is_none() {
                    // [COMMENT]: Live outbox job cần PROCESSING audit; snapshot replay không bắn result/DB write cho từng row.
                    let processing_report = JobExecutionResult {
                        job_id: job_id.clone(),
                        job_version: payload.job_version,
                        attempt: payload.attempt,
                        result_status: "PROCESSING".to_string(),
                        error_code: None,
                        message: "".to_string(),
                        job_topic: payload.job_topic.clone(),
                        source_domain: payload.source_domain.clone(),
                        trace_id: payload.trace_id.clone(),
                    };
                    let _ = JobResultReporter::report_outcome(
                        redis_job.client(),
                        &processing_report,
                    )
                    .await;
                }

                // Đo lường thời gian bắt đầu thực thi nghiệp vụ thô
                let start_time = tokio::time::Instant::now();

                // Thực thi định tuyến và gọi Executor nghiệp vụ, được giám sát bởi Watchdog bên ngoài
                let payload_dispatch = payload.clone();
                let worker_pool_dispatch = worker_pool.clone();
                let redis_job_dispatch = redis_job.clone();
                let zone_id = stream_key_clone.strip_prefix("jobs:").unwrap_or(&stream_key_clone).to_string();

                let exec_res = Ok(JobConsumer::dispatch_workload(
                    payload_dispatch,
                    worker_pool_dispatch,
                    redis_job_dispatch,
                    &zone_id,
                ).await);

                let duration_ms = start_time.elapsed().as_secs_f64() * 1000.0;
                let zone_id = stream_key_clone.strip_prefix("jobs:").unwrap_or(&stream_key_clone).to_string();

                // Phân loại kết quả thực thi
                let report = JobExecutionResult::from_outcome(
                    job_id.clone(),
                    payload.job_version,
                    payload.attempt,
                    payload.job_topic.clone(),
					payload.source_domain.clone(),
                    payload.trace_id.clone(),
                    exec_res,
                );

                let mut reconcile_failure_durable = true;
                if report.result_status == "FAILED" {
                    if let Some(generation) = payload.reconcile_generation {
                        // [COMMENT]: Completion marker của generation lỗi phải fail-close dù failed command đã XACK.
                        match redis_job
                            .client()
                            .get_multiplexed_async_connection()
                            .await
                        {
                            Ok(mut connection) => {
                                let result: redis::RedisResult<()> = redis::cmd("SET")
                                    .arg(format!(
                                        "mail:projection:reconcile_error:{zone_id}:{generation}"
                                    ))
                                    .arg(report.error_code.as_deref().unwrap_or("EXECUTION_FAILED"))
                                    .arg("EX")
                                    .arg(86_400)
                                    .query_async(&mut connection)
                                    .await;
                                reconcile_failure_durable = result.is_ok();
                            }
                            Err(_) => reconcile_failure_durable = false,
                        }
                    }
                }

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

                let terminal_result_durable = if payload.reconcile_generation.is_some()
                    && report.result_status != "RETRYABLE"
                {
                    // [COMMENT]: Reconcile success đã durable ở Zone KV; failure durable qua generation error marker.
                    true
                } else if report.result_status != "RETRYABLE" {
                    // [COMMENT]: Chỉ terminal result mới quay về JO; transient lỗi giữ command trong PEL.
                    match JobResultReporter::report_outcome(
                        redis_job.client(),
                        &report,
                    )
                    .await
                    {
                        Ok(()) => true,
                        Err(error) => {
                            Logger::sys_error(
                                "job.runner",
                                &format!("Không thể ghi terminal result cho job {}: {}", job_id, error),
                                "RESULT_STREAM_NOT_DURABLE",
                            );
                            false
                        }
                    }
                } else {
                    false
                };

                // [COMMENT]: Xác nhận giải phóng tin nhắn (XACK) trên Stream bằng group_name động tương ứng của payload
                if (report.result_status == "SUCCEEDED" || report.result_status == "FAILED")
                    && terminal_result_durable
                    && reconcile_failure_durable
                {
                    let ack_id = payload.redis_msg_id.as_deref().unwrap_or(&payload.job_id);
                    let group_name = payload.redis_group_name.as_deref().unwrap_or("dataplane-group");
                    let _ = crate::infra::redis::query::acknowledge_message(
                        redis_job.client(),
                        &stream_key_clone,
                        group_name,
                        ack_id,
                    )
                    .await;
                } else if report.result_status == "RETRYABLE" {
                    Logger::sys_warn(
                        "job.runner",
                        &format!("Job {} giữ trong Redis PEL để retry: {}", job_id, report.message),
                        "RETRYABLE_JOB_LEFT_PENDING",
                    );
                } else if !terminal_result_durable {
                    // [COMMENT]: Apply có thể đã xong; replay sau PEL idle sẽ trả DUPLICATE rồi ghi lại result.
                    Logger::sys_warn(
                        "job.runner",
                        &format!("Job {} giữ trong Redis PEL vì terminal result chưa durable", job_id),
                        "TERMINAL_RESULT_LEFT_PENDING",
                    );
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
            source_domain_for_registry,
            trace_id_for_registry,
            zone_lease,
        );

        // Kích hoạt tác vụ chạy sau khi đã đăng ký thành công
        let _ = tx.send(());
    }
}
