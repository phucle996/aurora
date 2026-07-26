use std::panic::AssertUnwindSafe;
use std::sync::atomic::AtomicUsize;
use std::sync::Arc;
use std::time::Duration;

use futures_util::FutureExt as _;
use opentelemetry::trace::{FutureExt as _, TraceContextExt};
use tokio::time::Instant;
use tokio_util::sync::CancellationToken;

use crate::infra::kafka::KafkaTransport;
use crate::infra::zone_kv::{ZoneKvStore, ZoneLease};
use crate::job_runtime::admission::release_admitted_job;
use crate::job_runtime::completion::{
    build_retry_request, complete_terminal, enqueue_retry, publish_result, CompletionStatus,
    JobExecutionResult, RetryRequest,
};
use crate::job_runtime::coordination::lease::{
    acquire_execution_lease, release_execution_lease, JOB_EXECUTION_LEASE_TTL_SECS,
};
use crate::job_runtime::coordination::watchdog::{JobExecutionLeaseRegistry, TrackedJobExecution};
use crate::job_runtime::model::{LeasedJob, QueuedJob, ValidatedJob};
use crate::observability::logger::{LogFields, Logger};

pub trait CleanupTaskSpawner: Send + Sync {
    fn spawn_tracked(
        &self,
        future: std::pin::Pin<Box<dyn std::future::Future<Output = ()> + Send + 'static>>,
    );
}

struct ExecutionCleanupGuard {
    zone_kv: Arc<ZoneKvStore>,
    lease: ZoneLease,
    admitted_jobs: Arc<AtomicUsize>,
    registry: Arc<JobExecutionLeaseRegistry>,
    registration_id: u64,
    cleanup_spawner: Arc<dyn CleanupTaskSpawner>,
    trace_context: opentelemetry::Context,
}

impl Drop for ExecutionCleanupGuard {
    fn drop(&mut self) {
        let zone_kv = self.zone_kv.clone();
        let lease = self.lease.clone();
        let lock_key = lease.key.clone();
        let admitted_jobs = self.admitted_jobs.clone();
        let registry = self.registry.clone();
        let cleanup_spawner = self.cleanup_spawner.clone();
        let trace_context = self.trace_context.clone();

        registry.remove_if_current(&lock_key, self.registration_id);
        release_admitted_job(&admitted_jobs, "execution_cleanup");

        // Drop must stay synchronous for cancellation/panic safety. The tracked
        // child performs best-effort release; TTL + fencing remain the final
        // protection if the process dies before this task completes.
        cleanup_spawner.spawn_tracked(Box::pin(
            async move {
                match release_execution_lease(&zone_kv, &lease).await {
                    Ok(true) => {}
                    Ok(false) => Logger::sys_warn_with_fields(
                        "job.execution.cleanup",
                        "JOB_LEASE_RELEASE_NOT_CURRENT",
                        "Lease cleanup observed a newer owner or an already released lease",
                        "",
                        LogFields {
                            operation_id: Some(&lock_key),
                            fencing_token: Some(lease.fencing_token),
                            outcome: Some("already_fenced"),
                            ..LogFields::default()
                        },
                    ),
                    Err(error) => Logger::sys_warn_with_fields(
                        "job.execution.cleanup",
                        "JOB_LEASE_RELEASE_FAILED",
                        "Lease cleanup failed; TTL and fencing protect against stale execution",
                        &error,
                        LogFields {
                            operation_id: Some(&lock_key),
                            fencing_token: Some(lease.fencing_token),
                            outcome: Some("ttl_expiry_required"),
                            ..LogFields::default()
                        },
                    ),
                }
            }
            .with_context(trace_context),
        ));
    }
}

pub struct JobExecutionRuntime {
    kafka: Arc<KafkaTransport>,
    zone_kv: Arc<ZoneKvStore>,
    registry: Arc<JobExecutionLeaseRegistry>,
    admitted_jobs: Arc<AtomicUsize>,
    retry_tx: tokio::sync::mpsc::Sender<RetryRequest>,
    zone_id: String,
    mail_runtime: Arc<crate::executor::mail::MailRuntime>,
    hypervisor_runtime: Arc<crate::executor::hypervisor::HypervisorRuntime>,
    cleanup_spawner: Arc<dyn CleanupTaskSpawner>,
    shutdown: CancellationToken,
}

pub struct JobExecutionDependencies {
    pub kafka: Arc<KafkaTransport>,
    pub zone_kv: Arc<ZoneKvStore>,
    pub registry: Arc<JobExecutionLeaseRegistry>,
    pub admitted_jobs: Arc<AtomicUsize>,
    pub retry_tx: tokio::sync::mpsc::Sender<RetryRequest>,
    pub zone_id: String,
    pub mail_runtime: Arc<crate::executor::mail::MailRuntime>,
    pub hypervisor_runtime: Arc<crate::executor::hypervisor::HypervisorRuntime>,
    pub cleanup_spawner: Arc<dyn CleanupTaskSpawner>,
    pub shutdown: CancellationToken,
}

impl JobExecutionRuntime {
    pub fn new(dependencies: JobExecutionDependencies) -> Self {
        Self {
            kafka: dependencies.kafka,
            zone_kv: dependencies.zone_kv,
            registry: dependencies.registry,
            admitted_jobs: dependencies.admitted_jobs,
            retry_tx: dependencies.retry_tx,
            zone_id: dependencies.zone_id,
            mail_runtime: dependencies.mail_runtime,
            hypervisor_runtime: dependencies.hypervisor_runtime,
            cleanup_spawner: dependencies.cleanup_spawner,
            shutdown: dependencies.shutdown,
        }
    }

    pub(crate) fn zone_kv(&self) -> &Arc<ZoneKvStore> {
        &self.zone_kv
    }

    /// Executes exactly one queued job while the calling worker owns its slot.
    pub async fn execute_job(&self, queued: QueuedJob) {
        if self.shutdown.is_cancelled() {
            release_admitted_job(&self.admitted_jobs, "shutdown_before_lease");
            return;
        }
        let lease_identity = format!("{}:{}", queued.job.source_domain, queued.job.resource_id);
        let lease_result = tokio::select! {
            biased;
            _ = self.shutdown.cancelled() => {
                release_admitted_job(&self.admitted_jobs, "shutdown_during_lease");
                return;
            }
            result = acquire_execution_lease(&self.zone_kv, &lease_identity) => result,
        };
        let lease = match lease_result {
            Ok(Some(lease)) => lease,
            Ok(None) => {
                let delay = Duration::from_millis(
                    JOB_EXECUTION_LEASE_TTL_SECS
                        .saturating_mul(1_000)
                        .saturating_add(rand::random::<u64>() % 2_000),
                );
                self.schedule_retry(
                    &queued,
                    queued.job.attempt,
                    delay,
                    "JOB_EXECUTION_LEASE_CONTENDED",
                    "",
                )
                .await;
                release_admitted_job(&self.admitted_jobs, "lease_contended");
                return;
            }
            Err(error) => {
                let delay =
                    Duration::from_millis(5_000_u64.saturating_add(rand::random::<u64>() % 1_000));
                self.schedule_retry(
                    &queued,
                    queued.job.attempt,
                    delay,
                    "JOB_EXECUTION_LEASE_ACQUIRE_FAILED",
                    &error,
                )
                .await;
                release_admitted_job(&self.admitted_jobs, "lease_acquire_failure");
                return;
            }
        };

        if self.shutdown.is_cancelled() {
            if let Err(error) = release_execution_lease(&self.zone_kv, &lease).await {
                Logger::sys_warn(
                    "job.execution",
                    &format!("Shutdown lease release failed; TTL will recover it: {error}"),
                    "JOB_SHUTDOWN_LEASE_RELEASE_FAILED",
                );
            }
            release_admitted_job(&self.admitted_jobs, "shutdown_after_lease");
            return;
        }

        let leased = LeasedJob { queued, lease };
        self.execute_leased_job(leased).await;
    }

    async fn execute_leased_job(&self, leased: LeasedJob) {
        let job = leased.queued.job.clone();
        let delivery = leased.queued.delivery.clone();
        let lock_key = leased.lease.key.clone();
        let execution_cancellation = CancellationToken::new();
        let execution_limit = job.execution_timeout();

        let registration_id = match self.registry.register_job_execution(
            lock_key.clone(),
            TrackedJobExecution::preparing(
                execution_limit,
                execution_cancellation.clone(),
                job.clone(),
                leased.lease.clone(),
                delivery.clone(),
            ),
        ) {
            Ok(registration_id) => registration_id,
            Err(error_code) => {
                release_admitted_job(&self.admitted_jobs, "watchdog_registration_failure");
                if let Err(error) = release_execution_lease(&self.zone_kv, &leased.lease).await {
                    Logger::sys_warn(
                        "job.execution",
                        &format!(
                            "Could not release lease after watchdog registration failure: {error}"
                        ),
                        "JOB_REGISTRATION_CLEANUP_FAILED",
                    );
                }
                Logger::sys_error(
                    "job.execution",
                    &format!(
                        "Job {} rejected because watchdog registration failed",
                        job.job_id
                    ),
                    error_code,
                );
                return;
            }
        };

        let parent_context = crate::observability::otel::OtelTracer::extract_context(
            &job.traceparent,
            &job.tracestate,
        );
        let job_context = crate::observability::otel::OtelTracer::start_span_with_parent(
            format!("process {}", job.job_topic),
            opentelemetry::trace::SpanKind::Consumer,
            vec![
                opentelemetry::KeyValue::new("messaging.system", "kafka"),
                opentelemetry::KeyValue::new("messaging.operation.type", "process"),
                opentelemetry::KeyValue::new("messaging.destination.name", delivery.topic.clone()),
                opentelemetry::KeyValue::new(
                    "messaging.kafka.partition",
                    i64::from(delivery.partition),
                ),
                opentelemetry::KeyValue::new("messaging.kafka.offset", delivery.offset),
                opentelemetry::KeyValue::new("aurora.job.id", job.job_id.clone()),
                opentelemetry::KeyValue::new("aurora.job.version", i64::from(job.job_version)),
                opentelemetry::KeyValue::new("aurora.job.attempt", i64::from(job.attempt)),
                opentelemetry::KeyValue::new("aurora.job.topic", job.job_topic.clone()),
                opentelemetry::KeyValue::new("aurora.zone.id", self.zone_id.clone()),
            ],
            &parent_context,
        );

        async {
            let _cleanup = ExecutionCleanupGuard {
                zone_kv: self.zone_kv.clone(),
                lease: leased.lease.clone(),
                admitted_jobs: self.admitted_jobs.clone(),
                registry: self.registry.clone(),
                registration_id,
                cleanup_spawner: self.cleanup_spawner.clone(),
                trace_context: opentelemetry::Context::current(),
            };

            Logger::sys_info(
                "job.execution",
                &format!(
                    "Starting job {} topic={} deadline={execution_limit:?}",
                    job.job_id, job.job_topic
                ),
            );

            if job.reconcile_generation.is_none() {
                let processing = JobExecutionResult::processing(&job);
                if let Err(error) = publish_result(&self.kafka, &processing).await {
                    Logger::sys_warn_with_fields(
                        "job.execution.processing",
                        "JOB_PROCESSING_REPORT_PUBLISH_FAILED",
                        "PROCESSING audit failed; terminal completion remains the settlement boundary",
                        &error,
                        LogFields {
                            operation_id: Some(&job.job_id),
                            job_version: Some(u64::from(job.job_version)),
                            retryable: Some(true),
                            outcome: Some("audit_missing"),
                            ..LogFields::default()
                        },
                    );
                }
            }

            if self.shutdown.is_cancelled() {
                Logger::sys_info(
                    "job.execution",
                    "Shutdown arrived before executor start; Kafka source remains unsettled",
                );
                finish_current_span("JOB_EXECUTION_SKIPPED_ON_SHUTDOWN", false, false);
                return;
            }

            // Deadline starts only after PROCESSING publish resolves. The
            // watchdog renews the lease during preparation, but cannot emit a
            // terminal timeout that would race a late PROCESSING event.
            if !self
                .registry
                .mark_execution_phase(&lock_key, registration_id)
            {
                Logger::sys_warn(
                    "job.execution",
                    &format!(
                        "Job {} lost its watchdog registration before executor start; source remains unsettled",
                        job.job_id
                    ),
                    "JOB_EXECUTION_FENCE_LOST",
                );
                finish_current_span("JOB_EXECUTION_FENCE_LOST", false, false);
                return;
            }

            let started_at = Instant::now();
            let workload = AssertUnwindSafe(execute_workload(
                job.clone(),
                self.mail_runtime.clone(),
                self.hypervisor_runtime.clone(),
                &self.zone_id,
                self.zone_kv.clone(),
            ))
            .catch_unwind();
            let executor_result = tokio::select! {
                biased;
                _ = execution_cancellation.cancelled() => {
                    Logger::sys_warn(
                        "job.execution",
                        &format!("Execution for job {} was cancelled by its fencing watchdog", job.job_id),
                        "JOB_EXECUTION_CANCELLED",
                    );
                    finish_current_span("JOB_EXECUTION_CANCELLED", false, false);
                    return;
                }
                result = workload => result,
            };

            // The external side-effect boundary is complete. Keep renewing its
            // lease until result/retry settlement finishes, but disable the
            // execution deadline so timeout and terminal result cannot race.
            if !self
                .registry
                .mark_completion_phase(&lock_key, registration_id)
            {
                Logger::sys_warn(
                    "job.execution",
                    &format!(
                        "Job {} lost its watchdog registration before completion; source remains unsettled",
                        job.job_id
                    ),
                    "JOB_EXECUTION_FENCE_LOST",
                );
                finish_current_span("JOB_EXECUTION_FENCE_LOST", false, false);
                return;
            }

            let executor_result = match executor_result {
                Ok(result) => result,
                Err(_) => {
                    Logger::sys_error(
                        "job.execution",
                        &format!(
                            "Executor panicked for job {}; source remains unsettled for safe replay",
                            job.job_id
                        ),
                        "JOB_EXECUTOR_PANICKED",
                    );
                    finish_current_span("JOB_EXECUTOR_PANICKED", false, false);
                    return;
                }
            };
            let duration_ms = started_at.elapsed().as_secs_f64() * 1_000.0;
            let mut result = JobExecutionResult::from_executor(&job, executor_result);

            if result.status == CompletionStatus::Retryable {
                let next_attempt = job.attempt.saturating_add(1);
                if next_attempt >= crate::config::Config::get_global().kafka_max_job_attempts {
                    result.mark_retry_exhausted();
                } else {
                    let delay = Duration::from_millis(
                        100_u64
                            .saturating_mul(1_u64 << job.attempt.min(8))
                            .min(30_000)
                            .saturating_add(rand::random::<u64>() % 250),
                    );
                    let scheduled = self
                        .schedule_retry(
                            &leased.queued,
                            next_attempt,
                            delay,
                            "JOB_EXECUTOR_RETRYABLE",
                            &result.message,
                        )
                        .await;
                    crate::observability::metrics::JobExecutionMetrics::record_attempt(
                        &self.zone_id,
                        &job.job_topic,
                        if scheduled {
                            "RETRY_SCHEDULED"
                        } else {
                            "RETRY_UNSCHEDULED"
                        },
                        duration_ms,
                    );
                    finish_current_span(
                        if scheduled {
                            "JOB_RETRY_SCHEDULED"
                        } else {
                            "JOB_RETRY_NOT_SCHEDULED"
                        },
                        false,
                        false,
                    );
                    return;
                }
            }

            let completion =
                complete_terminal(&self.kafka, &job, &delivery, &result).await;
            crate::observability::metrics::JobExecutionMetrics::record_attempt(
                &self.zone_id,
                &job.job_topic,
                result.status.as_str(),
                duration_ms,
            );

            if result.status == CompletionStatus::Succeeded {
                Logger::job_log(
                    &job.job_id,
                    &job.job_topic,
                    job.attempt,
                    "job.success",
                    &result.message,
                );
            } else {
                Logger::sys_error(
                    "job.execution",
                    &format!("Workload failed for job {}: {}", job.job_id, result.message),
                    result.error_code.as_deref().unwrap_or("UNKNOWN"),
                );
            }
            finish_current_span(
                if result.status == CompletionStatus::Succeeded
                    && completion.result_durable
                    && completion.source_settled
                {
                    ""
                } else {
                    result
                        .error_code
                        .as_deref()
                        .unwrap_or("JOB_ATTEMPT_NOT_DURABLE")
                },
                completion.result_durable,
                completion.source_settled,
            );
        }
        .with_context(job_context)
        .await;
    }

    async fn schedule_retry(
        &self,
        queued: &QueuedJob,
        next_attempt: u32,
        delay: Duration,
        event_code: &'static str,
        error: &str,
    ) -> bool {
        let request = build_retry_request(queued, next_attempt, delay, event_code);
        match enqueue_retry(&self.retry_tx, request).await {
            Ok(()) => {
                crate::observability::metrics::WorkerControlMetrics::record_job_runtime_event(
                    &self.zone_id,
                    "retry_scheduled",
                );
                Logger::sys_warn_with_fields(
                    "job.execution.retry",
                    event_code,
                    "Bounded retry scheduled; source remains unsettled until retry publish is durable",
                    error,
                    LogFields {
                        operation_id: Some(&queued.job.job_id),
                        retryable: Some(true),
                        outcome: Some("retry_scheduled"),
                        ..LogFields::default()
                    },
                );
                true
            }
            Err(queue_error) => {
                crate::observability::metrics::WorkerControlMetrics::record_job_runtime_event(
                    &self.zone_id,
                    "retry_queue_unavailable",
                );
                Logger::sys_error(
                    "job.execution.retry",
                    &format!(
                        "Retry queue unavailable for job {}; source remains unsettled: {queue_error}",
                        queued.job.job_id
                    ),
                    "JOB_RETRY_QUEUE_UNAVAILABLE",
                );
                false
            }
        }
    }
}

async fn execute_workload(
    job: Arc<ValidatedJob>,
    mail_runtime: Arc<crate::executor::mail::MailRuntime>,
    hypervisor_runtime: Arc<crate::executor::hypervisor::HypervisorRuntime>,
    zone_id: &str,
    zone_kv: Arc<ZoneKvStore>,
) -> Result<crate::executor::ExecutionResult, crate::executor::ExecutorError> {
    let job_topic = job.job_topic.clone();
    let Some((workload, action)) = job_topic.split_once('.') else {
        return Err(crate::executor::ExecutorError::ExecutionFailed(format!(
            "Invalid job topic format: {}",
            job_topic
        )));
    };
    match workload {
        "mail" => {
            crate::executor::mail::dispatch_mail_job(action, job, mail_runtime, zone_id).await
        }
        "hypervisor" => {
            crate::executor::hypervisor::dispatch_hypervisor_job(action, job, hypervisor_runtime)
                .await
        }
        "storage" => crate::executor::storage::dispatch_storage_job(action, job, zone_kv).await,
        _ => Err(crate::executor::ExecutorError::ExecutionFailed(format!(
            "Unsupported workload type: {workload}"
        ))),
    }
}

fn finish_current_span(error_code: &str, result_durable: bool, source_settled: bool) {
    let context = opentelemetry::Context::current();
    let span = context.span();
    span.set_attribute(opentelemetry::KeyValue::new(
        "aurora.result.durable",
        result_durable,
    ));
    span.set_attribute(opentelemetry::KeyValue::new(
        "messaging.settlement.success",
        source_settled,
    ));
    crate::observability::otel::OtelTracer::finish_span(
        &context,
        (!error_code.is_empty()).then_some(error_code),
    );
}
