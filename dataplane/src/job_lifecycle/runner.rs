use crate::infra::kafka::{transport_proto::JobCommandV1, KafkaTransport};
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
        runtime_redis: Arc<RedisClientManager>,
        kafka: Arc<KafkaTransport>,
        zone_kv: Arc<ZoneKvStore>,
        active_lock_registry: Arc<crate::workerpool::watchdog::ActiveLockRegistry>,
        active_jobs: Arc<AtomicUsize>,
        zone_id: String,
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
        let kafka_delivery_for_registry = payload.kafka_delivery.clone();

        let (tx, rx) = tokio::sync::oneshot::channel();

        // [COMMENT]: Job task không thuộc worker receive-loop; guard riêng giữ nó trong graceful-shutdown barrier.
        let task_guard = worker_pool.track_task();

        let handle = tokio::spawn(async move {
            let _task_guard = task_guard;
            // Chờ tín hiệu đăng ký hoàn tất vào Watchdog Registry để tránh race condition deregister trước register
            let _ = rx.await;

            let trace_id = payload.trace_id.clone();
            let zone_id = zone_id.clone();

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
                        &kafka,
                        &processing_report,
                    )
                    .await;
                }

                // Đo lường thời gian bắt đầu thực thi nghiệp vụ thô
                let start_time = tokio::time::Instant::now();

                // Thực thi định tuyến và gọi Executor nghiệp vụ, được giám sát bởi Watchdog bên ngoài
                let payload_dispatch = payload.clone();
                let worker_pool_dispatch = worker_pool.clone();
                let runtime_redis_dispatch = runtime_redis.clone();

                let exec_res = Ok(JobConsumer::dispatch_workload(
                    payload_dispatch,
                    worker_pool_dispatch,
                    runtime_redis_dispatch,
                    &zone_id,
                ).await);

                let duration_ms = start_time.elapsed().as_secs_f64() * 1000.0;

                // Phân loại kết quả thực thi
                let mut report = JobExecutionResult::from_outcome(
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
                        match runtime_redis
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

                let mut retry_requeued_durable = false;
                if report.result_status == "RETRYABLE" {
                    if payload.attempt.saturating_add(1)
                        >= crate::config::Config::get_global().kafka_max_job_attempts
                    {
                        report.result_status = "FAILED".to_string();
                        report.error_code = Some("RETRY_EXHAUSTED".to_string());
                    } else if let Some(delivery) = payload.kafka_delivery.as_ref() {
                        // [COMMENT]: Kafka không có PEL; retry là record mới durable trước khi commit record hiện tại.
                        let backoff_ms = 100_u64
                            .saturating_mul(1_u64 << payload.attempt.min(8))
                            .min(30_000)
                            .saturating_add(rand::random::<u64>() % 250);
                        tokio::time::sleep(Duration::from_millis(backoff_ms)).await;
                        let command = JobCommandV1 {
                            job_id: uuid::Uuid::parse_str(&payload.job_id)
                                .map(|value| value.as_bytes().to_vec())
                                .unwrap_or_default(),
                            job_version: payload.job_version,
                            attempt: payload.attempt.saturating_add(1),
                            job_topic: payload.job_topic.clone(),
                            source_domain: payload.source_domain.clone(),
                            resource_id: payload.resource_id.clone(),
                            payload_schema_version: payload.payload_schema_version,
                            payload: payload.payload.clone(),
                            trace_id: (0..payload.trace_id.len())
                                .step_by(2)
                                .filter_map(|index| {
                                    payload.trace_id.get(index..index.saturating_add(2))
                                })
                                .filter_map(|pair| u8::from_str_radix(pair, 16).ok())
                                .collect(),
                            idle_seconds: payload.idle,
                            reconcile_generation: payload.reconcile_generation,
                            target_zone_id: payload.target_zone_id.clone(),
                            transport_schema_version: 1,
                        };
                        retry_requeued_durable = kafka
                            .publish_message(&delivery.topic, &command.job_id, &command)
                            .await
                            .is_ok();
                    }
                }

                let terminal_result_durable = if retry_requeued_durable {
                    true
                } else if payload.reconcile_generation.is_some()
                    && report.result_status != "RETRYABLE"
                {
                    // [COMMENT]: Reconcile success đã durable ở Zone KV; failure durable qua generation error marker.
                    true
                } else if report.result_status != "RETRYABLE" {
                    // [COMMENT]: Chỉ terminal result mới quay về JO; transient lỗi giữ command trong PEL.
                    match JobResultReporter::report_outcome(
                        &kafka,
                        &report,
                    )
                    .await
                    {
                        Ok(()) => true,
                        Err(error) => {
                            Logger::sys_error(
                                "job.runner",
                                &format!("Không thể ghi terminal result cho job {}: {}", job_id, error),
                                "KAFKA_RESULT_NOT_DURABLE",
                            );
                            false
                        }
                    }
                } else {
                    false
                };

                // [COMMENT]: Commit Kafka offset chỉ sau terminal result hoặc retry record đã acks=all.
                if (report.result_status == "SUCCEEDED"
                    || report.result_status == "FAILED"
                    || retry_requeued_durable)
                    && terminal_result_durable
                    && reconcile_failure_durable
                {
                    if let Some(delivery) = payload.kafka_delivery.as_ref() {
                        if let Err(error) = delivery.settle().await {
                            Logger::sys_warn(
                                "job.runner",
                                &format!("Kafka offset remains uncommitted for {}: {error}", job_id),
                                "KAFKA_OFFSET_COMMIT_DEFERRED",
                            );
                        }
                    }
                } else if report.result_status == "RETRYABLE" {
                    Logger::sys_warn(
                        "job.runner",
                        &format!("Job {} retry chưa durable trên Kafka: {}", job_id, report.message),
                        "KAFKA_RETRY_NOT_DURABLE",
                    );
                } else if !terminal_result_durable {
                    // [COMMENT]: Apply có thể đã xong; Kafka replay trả DUPLICATE rồi ghi lại result.
                    Logger::sys_warn(
                        "job.runner",
                        &format!("Job {} giữ Kafka offset vì terminal result chưa durable", job_id),
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
            kafka_delivery_for_registry,
        );

        // Kích hoạt tác vụ chạy sau khi đã đăng ký thành công
        let _ = tx.send(());
    }
}
