use crate::infra::kafka::{
    transport_proto::{DeadLetterRecordV1, JobCommandV1},
    KafkaTransport,
};
use crate::infra::zone_kv::{ZoneKvStore, ZoneLease};
// Sử dụng các module nội bộ từ job_lifecycle mới đổi tên
use crate::job_lifecycle::consumer::JobConsumer;
use crate::job_lifecycle::lease::{
    acquire_job_execution_lease, build_job_execution_lease_retry, JobExecutionLeaseRetry,
    JOB_EXECUTION_LEASE_TTL_SECS,
};
use crate::job_lifecycle::message::JobPayload;
use crate::job_lifecycle::result::{JobExecutionResult, JobResultReporter};
use crate::observability::logger::{LogFields, Logger};
use crate::workerpool::pool::WorkerLifecycleManager;
use opentelemetry::trace::FutureExt;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use std::time::Duration;
use tokio::time::Instant;

/// Guard tự động giải phóng khóa phân phối Lease Lock và giảm bộ đếm admitted_jobs
/// khi khối xử lý kết thúc (hoặc bị hủy/timeout/panic), ngăn chặn rò rỉ tài nguyên.
struct ExecutionCleanupGuard {
    zone_kv: Arc<ZoneKvStore>,
    lease: ZoneLease,
    admitted_jobs: Arc<AtomicUsize>,
    job_execution_lease_registry: Arc<crate::workerpool::lease_watchdog::JobExecutionLeaseRegistry>,
    registration_id: u64,
    task_tracker: Arc<crate::workerpool::pool::TaskTracker>,
    trace_context: opentelemetry::Context,
}

impl Drop for ExecutionCleanupGuard {
    fn drop(&mut self) {
        let zone_kv = self.zone_kv.clone();
        let lease = self.lease.clone();
        let lock = lease.key.clone();
        let admitted_jobs = self.admitted_jobs.clone();
        let registry = self.job_execution_lease_registry.clone();
        let task_tracker = self.task_tracker.clone();
        let trace_context = self.trace_context.clone();

        // Đồng bộ xóa khỏi Registry ngay lập tức để chặn chu kỳ watchdog tiếp theo gia hạn khóa
        registry.remove_if_current(&lock, self.registration_id);
        // [COMMENT]: Local admission không phụ thuộc network; NATS chậm/mất kết nối không được giữ sai active counter.
        admitted_jobs.fetch_sub(1, Ordering::SeqCst);

        // Spawn một task độc lập để giải phóng tài nguyên bất đồng bộ ngoài tầm của task bị hủy
        let cleanup_task_guard = task_tracker.track();
        tokio::spawn(
            async move {
                let _cleanup_task_guard = cleanup_task_guard;
                match zone_kv.release_lease(&lease).await {
                    Ok(true) => {}
                    Ok(false) => Logger::sys_warn_with_fields(
                        "job.cleanup",
                        "JOB_LEASE_RELEASE_NOT_CURRENT",
                        "Job lease cleanup observed a newer owner or an already released lease",
                        "",
                        LogFields {
                            operation_id: Some(&lock),
                            fencing_token: Some(lease.fencing_token),
                            outcome: Some("already_fenced"),
                            ..LogFields::default()
                        },
                    ),
                    Err(error) => Logger::sys_warn_with_fields(
                        "job.cleanup",
                        "JOB_LEASE_RELEASE_FAILED",
                        "Job lease cleanup failed; TTL and fencing prevent stale execution",
                        &error,
                        LogFields {
                            operation_id: Some(&lock),
                            fencing_token: Some(lease.fencing_token),
                            retryable: Some(false),
                            outcome: Some("ttl_expiry_required"),
                            ..LogFields::default()
                        },
                    ),
                }
            }
            .with_context(trace_context),
        );
    }
}

pub struct JobRunner;

pub struct JobRunnerContext {
    kafka: Arc<KafkaTransport>,
    zone_kv: Arc<ZoneKvStore>,
    job_execution_lease_registry: Arc<crate::workerpool::lease_watchdog::JobExecutionLeaseRegistry>,
    admitted_jobs: Arc<AtomicUsize>,
    job_execution_lease_retry_tx: tokio::sync::mpsc::Sender<JobExecutionLeaseRetry>,
    zone_id: String,
}

impl JobRunnerContext {
    pub fn new(
        kafka: Arc<KafkaTransport>,
        zone_kv: Arc<ZoneKvStore>,
        job_execution_lease_registry: Arc<
            crate::workerpool::lease_watchdog::JobExecutionLeaseRegistry,
        >,
        admitted_jobs: Arc<AtomicUsize>,
        job_execution_lease_retry_tx: tokio::sync::mpsc::Sender<JobExecutionLeaseRetry>,
        zone_id: String,
    ) -> Self {
        Self {
            kafka,
            zone_kv,
            job_execution_lease_registry,
            admitted_jobs,
            job_execution_lease_retry_tx,
            zone_id,
        }
    }

    pub(crate) fn zone_kv(&self) -> &Arc<ZoneKvStore> {
        &self.zone_kv
    }
}

impl JobRunner {
    /// Execute one job while the calling worker owns the concurrency slot.
    pub async fn run_job(
        mut payload: JobPayload,
        worker_pool: Arc<WorkerLifecycleManager>,
        context: Arc<JobRunnerContext>,
    ) {
        let kafka = context.kafka.clone();
        let zone_kv = context.zone_kv.clone();
        let job_execution_lease_registry = context.job_execution_lease_registry.clone();
        let admitted_jobs = context.admitted_jobs.clone();
        let job_execution_lease_retry_tx = context.job_execution_lease_retry_tx.clone();
        let zone_id = context.zone_id.clone();
        let zone_lease = match acquire_job_execution_lease(&zone_kv, &payload.job_id).await {
            Ok(Some(lease)) => lease,
            Ok(None) => {
                let delay = Duration::from_millis(
                    JOB_EXECUTION_LEASE_TTL_SECS
                        .saturating_mul(1_000)
                        .saturating_add(rand::random::<u64>() % 2_000),
                );
                schedule_job_execution_lease_retry(
                    &payload,
                    &job_execution_lease_retry_tx,
                    delay,
                    "JOB_EXECUTION_LEASE_CONTENDED",
                    "",
                );
                admitted_jobs.fetch_sub(1, Ordering::SeqCst);
                return;
            }
            Err(error) => {
                let delay =
                    Duration::from_millis(5_000_u64.saturating_add(rand::random::<u64>() % 1_000));
                schedule_job_execution_lease_retry(
                    &payload,
                    &job_execution_lease_retry_tx,
                    delay,
                    "JOB_EXECUTION_LEASE_ACQUIRE_FAILED",
                    &error,
                );
                admitted_jobs.fetch_sub(1, Ordering::SeqCst);
                return;
            }
        };
        payload.zone_lease = Some(zone_lease.clone());
        let lock_key = zone_lease.key.clone();

        // Hạn mức thực thi tối đa lấy từ idle (mặc định 10 phút nếu không chỉ định)
        let limit = payload
            .idle
            .map(|s| Duration::from_secs(s as u64))
            .unwrap_or_else(|| Duration::from_secs(600));

        let lock_key_clone = lock_key.clone();
        let job_execution_lease_registry_for_task = job_execution_lease_registry.clone();
        let job_id_clone = payload.job_id.clone();
        let job_version = payload.job_version;
        let attempt = payload.attempt;
        let job_topic_for_registry = payload.job_topic.clone();
        let source_domain_for_registry = payload.source_domain.clone();
        let trace_id_for_registry = payload.trace_id.clone();
        let zone_lease_for_task = zone_lease.clone();
        let kafka_delivery_for_registry = payload.kafka_delivery.clone();
        let zone_kv_for_task = zone_kv.clone();
        let admitted_jobs_for_task = admitted_jobs.clone();
        let task_tracker_for_cleanup = worker_pool.task_tracker();

        let (tx, rx) = tokio::sync::oneshot::channel::<u64>();

        let handle = tokio::spawn(async move {
            // Chờ tín hiệu đăng ký hoàn tất vào Watchdog Registry để tránh race condition deregister trước register
            let Ok(registration_id) = rx.await else {
                return;
            };

            let zone_id = zone_id.clone();
            let parent_context = crate::observability::otel::OtelTracer::extract_context(
                &payload.traceparent,
                &payload.tracestate,
            );
            let mut span_attributes = vec![
                opentelemetry::KeyValue::new("messaging.system", "kafka"),
                opentelemetry::KeyValue::new("messaging.operation.type", "process"),
                opentelemetry::KeyValue::new("aurora.job.id", payload.job_id.clone()),
                opentelemetry::KeyValue::new("aurora.job.version", i64::from(payload.job_version)),
                opentelemetry::KeyValue::new("aurora.job.attempt", i64::from(payload.attempt)),
                opentelemetry::KeyValue::new("aurora.job.topic", payload.job_topic.clone()),
                opentelemetry::KeyValue::new("aurora.zone.id", zone_id.clone()),
            ];
            if let Some(delivery) = payload.kafka_delivery.as_ref() {
                span_attributes.extend([
                    opentelemetry::KeyValue::new(
                        "messaging.destination.name",
                        delivery.topic.clone(),
                    ),
                    opentelemetry::KeyValue::new("messaging.kafka.offset", delivery.offset),
                    opentelemetry::KeyValue::new(
                        "messaging.kafka.partition",
                        i64::from(delivery.partition),
                    ),
                ]);
            }
            let job_context = crate::observability::otel::OtelTracer::start_span_with_parent(
                format!("process {}", payload.job_topic),
                opentelemetry::trace::SpanKind::Consumer,
                span_attributes,
                &parent_context,
            );

            async move {
                use opentelemetry::trace::TraceContextExt;
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
                    zone_kv: zone_kv_for_task.clone(),
                    lease: zone_lease_for_task.clone(),
                    admitted_jobs: admitted_jobs_for_task.clone(),
                    job_execution_lease_registry:
                        job_execution_lease_registry_for_task.clone(),
                    registration_id,
                    task_tracker: task_tracker_for_cleanup.clone(),
                    trace_context: opentelemetry::Context::current(),
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
                    if let Err(error) =
                        JobResultReporter::report_outcome(&kafka, &processing_report).await
                    {
                        Logger::sys_warn_with_fields(
                            "job.runner.processing_report",
                            "JOB_PROCESSING_REPORT_PUBLISH_FAILED",
                            "Could not publish PROCESSING audit; terminal result remains the settlement boundary",
                            &error,
                            LogFields {
                                operation_id: Some(&job_id),
                                job_version: Some(payload.job_version as u64),
                                retryable: Some(true),
                                outcome: Some("audit_missing"),
                                ..LogFields::default()
                            },
                        );
                    }
                }

                // Đo lường thời gian bắt đầu thực thi nghiệp vụ thô
                let start_time = tokio::time::Instant::now();

                // Thực thi định tuyến và gọi Executor nghiệp vụ, được giám sát bởi Watchdog bên ngoài
                let payload_dispatch = payload.clone();
                let worker_pool_dispatch = worker_pool.clone();

                let exec_res = Ok(JobConsumer::dispatch_workload(
                    payload_dispatch,
                    worker_pool_dispatch,
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
                if report.result_status == "FAILED"
                    && payload.reconcile_generation.is_some()
                {
                    // [COMMENT]: Dataplane không có Redis trung tâm. Reconcile failure phải vào
                    // Kafka DLQ acks=all trước khi command offset được commit.
                    reconcile_failure_durable =
                        if let Some(delivery) = payload.kafka_delivery.as_ref() {
                            let dead_letter = DeadLetterRecordV1 {
                                event_id: uuid::Uuid::new_v4().as_bytes().to_vec(),
                                source_topic: delivery.topic.clone(),
                                source_partition: delivery.partition,
                                source_offset: delivery.offset,
                                error_code: report.error_code.clone().unwrap_or_else(|| {
                                    "MAIL_RECONCILE_EXECUTION_FAILED".to_string()
                                }),
                                error_message: report.message.chars().take(1_024).collect(),
                                original_payload: payload.payload.to_vec(),
                                failed_at_unix_ms: chrono::Utc::now().timestamp_millis(),
                                schema_version: 1,
                            };
                            let event_key = dead_letter.event_id.clone();
                            kafka
                                .publish_message(
                                    &kafka.dead_letter_topic(),
                                    &event_key,
                                    &dead_letter,
                                )
                                .await
                                .is_ok()
                        } else {
                            false
                        };
                }

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
                        let retry_context =
                            crate::observability::otel::OtelTracer::start_current_span(
                                format!("send {}", delivery.topic),
                                opentelemetry::trace::SpanKind::Producer,
                                vec![
                                    opentelemetry::KeyValue::new("messaging.system", "kafka"),
                                    opentelemetry::KeyValue::new(
                                        "messaging.operation.type",
                                        "send",
                                    ),
                                    opentelemetry::KeyValue::new(
                                        "messaging.destination.name",
                                        delivery.topic.clone(),
                                    ),
                                    opentelemetry::KeyValue::new(
                                        "aurora.job.attempt",
                                        i64::from(payload.attempt.saturating_add(1)),
                                    ),
                                ],
                            );
                        let propagation =
                            crate::observability::otel::OtelTracer::inject_context(&retry_context);
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
                            payload: payload.payload.to_vec(),
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
                            traceparent: propagation.traceparent,
                            tracestate: propagation.tracestate,
                        };
                        let retry_result = kafka
                            .publish_message(&delivery.topic, &command.job_id, &command)
                            .with_context(retry_context.clone())
                            .await;
                        retry_requeued_durable = retry_result.is_ok();
                        crate::observability::otel::OtelTracer::finish_span(
                            &retry_context,
                            retry_result
                                .as_ref()
                                .err()
                                .map(|_| "KAFKA_RETRY_PUBLISH_FAILED"),
                        );
                    }
                }

                let metric_status = if retry_requeued_durable {
                    "REQUEUED"
                } else {
                    report.result_status.as_str()
                };
                crate::observability::metrics::JobExecutionMetrics::record_attempt(
                    &zone_id,
                    &payload.job_topic,
                    metric_status,
                    duration_ms,
                );

                let terminal_result_durable = if retry_requeued_durable {
                    true
                } else if payload.reconcile_generation.is_some()
                    && report.result_status != "RETRYABLE"
                {
                    // [COMMENT]: Reconcile success đã durable ở Zone KV; failure durable qua Kafka DLQ.
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

                let mut source_settled = payload.kafka_delivery.is_none();
                // [COMMENT]: Commit Kafka offset chỉ sau terminal result hoặc retry record đã acks=all.
                if (report.result_status == "SUCCEEDED"
                    || report.result_status == "FAILED"
                    || retry_requeued_durable)
                    && terminal_result_durable
                    && reconcile_failure_durable
                {
                    if let Some(delivery) = payload.kafka_delivery.as_ref() {
                        let settle_result =
                            crate::observability::otel::OtelTracer::trace_result(
                                format!("commit {}", delivery.topic),
                                opentelemetry::trace::SpanKind::Client,
                                vec![
                                    opentelemetry::KeyValue::new("messaging.system", "kafka"),
                                    opentelemetry::KeyValue::new(
                                        "messaging.operation.type",
                                        "settle",
                                    ),
                                    opentelemetry::KeyValue::new(
                                        "messaging.destination.name",
                                        delivery.topic.clone(),
                                    ),
                                    opentelemetry::KeyValue::new(
                                        "messaging.kafka.partition",
                                        i64::from(delivery.partition),
                                    ),
                                    opentelemetry::KeyValue::new(
                                        "messaging.kafka.offset",
                                        delivery.offset,
                                    ),
                                ],
                                delivery.settle(),
                            )
                            .await;
                        if let Err(error) = settle_result {
                            Logger::sys_warn(
                                "job.runner",
                                &format!("Kafka offset remains uncommitted for {}: {error}", job_id),
                                "KAFKA_OFFSET_COMMIT_DEFERRED",
                            );
                        } else {
                            source_settled = true;
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
                let current_context = opentelemetry::Context::current();
                let current_span = current_context.span();
                current_span.set_attribute(opentelemetry::KeyValue::new(
                    "aurora.job.outcome",
                    report.result_status.clone(),
                ));
                current_span.set_attribute(opentelemetry::KeyValue::new(
                    "aurora.result.durable",
                    terminal_result_durable,
                ));
                current_span.set_attribute(opentelemetry::KeyValue::new(
                    "messaging.settlement.success",
                    source_settled,
                ));
                crate::observability::otel::OtelTracer::finish_span(
                    &current_context,
                    if report.result_status == "SUCCEEDED"
                        && terminal_result_durable
                        && source_settled
                    {
                        None
                    } else {
                        Some(
                            report
                                .error_code
                                .as_deref()
                                .unwrap_or("JOB_ATTEMPT_NOT_DURABLE"),
                        )
                    },
                );
            }
            .with_context(job_context)
            .await;
        });

        // Đăng ký tác vụ và AbortHandle vào Watchdog để giám sát vòng đời
        let registration_id = match job_execution_lease_registry.register_job_execution(
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
            zone_lease.clone(),
            kafka_delivery_for_registry,
        ) {
            Ok(registration_id) => registration_id,
            Err(error_code) => {
                // A job without a watchdog registration must never execute:
                // there would be no lease renewal or timeout fence.
                handle.abort();
                admitted_jobs.fetch_sub(1, Ordering::SeqCst);
                if let Err(error) = zone_kv.release_lease(&zone_lease).await {
                    Logger::sys_warn(
                        "job.runner",
                        &format!(
                            "Could not release lease after watchdog registration failure: {error}"
                        ),
                        "JOB_REGISTRATION_CLEANUP_FAILED",
                    );
                }
                Logger::sys_error(
                    "job.runner",
                    &format!(
                        "Job for lock {lock_key} rejected because its watchdog registration failed"
                    ),
                    error_code,
                );
                return;
            }
        };

        // Kích hoạt tác vụ chạy sau khi đã đăng ký thành công
        if tx.send(registration_id).is_err() {
            job_execution_lease_registry.remove_if_current(&lock_key, registration_id);
            admitted_jobs.fetch_sub(1, Ordering::SeqCst);
            if let Err(error) = zone_kv.release_lease(&zone_lease).await {
                Logger::sys_warn(
                    "job.runner",
                    &format!("Could not release lease after job startup failure: {error}"),
                    "JOB_STARTUP_CLEANUP_FAILED",
                );
            }
        }

        // Awaiting the execution task is the worker-pool concurrency boundary.
        // A watchdog abort completes this await with a cancelled JoinError.
        if let Err(join_error) = handle.await {
            if !join_error.is_cancelled() {
                Logger::sys_error(
                    "job.runner",
                    &format!("Job execution task panicked: {join_error}"),
                    "JOB_EXECUTION_TASK_PANICKED",
                );
            }
        }
    }
}

fn schedule_job_execution_lease_retry(
    payload: &JobPayload,
    retry_tx: &tokio::sync::mpsc::Sender<JobExecutionLeaseRetry>,
    delay: Duration,
    event_code: &'static str,
    error: &str,
) {
    let request = match build_job_execution_lease_retry(payload, delay) {
        Ok(request) => request,
        Err(build_error) => {
            Logger::sys_error(
                "job.execution_lease",
                "Could not build a durable retry after job execution lease acquisition failed; source remains unsettled",
                build_error,
            );
            return;
        }
    };
    match retry_tx.try_send(request) {
        Ok(()) => {
            let metric_event = if event_code == "JOB_EXECUTION_LEASE_CONTENDED" {
                "contention_retry_scheduled"
            } else {
                "acquire_error_retry_scheduled"
            };
            crate::observability::metrics::WorkerControlMetrics::record_job_execution_lease_event(
                &crate::config::Config::get_global().zone_id,
                metric_event,
            );
            Logger::sys_warn_with_fields(
                "job.execution_lease",
                event_code,
                "Job execution lease unavailable; bounded retry was scheduled without settling the source",
                error,
                LogFields {
                    operation_id: Some(&payload.job_id),
                    retryable: Some(true),
                    outcome: Some("retry_scheduled"),
                    ..LogFields::default()
                },
            );
        }
        Err(queue_error) => {
            crate::observability::metrics::WorkerControlMetrics::record_job_execution_lease_event(
                &crate::config::Config::get_global().zone_id,
                "retry_queue_unavailable",
            );
            Logger::sys_error(
                "job.execution_lease",
                &format!(
                    "Job execution lease retry queue unavailable; source remains unsettled: {queue_error}"
                ),
                "JOB_EXECUTION_LEASE_RETRY_QUEUE_UNAVAILABLE",
            );
        }
    }
}
