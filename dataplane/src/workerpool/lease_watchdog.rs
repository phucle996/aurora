use crate::infra::kafka::KafkaDelivery;
use crate::infra::zone_kv::{ZoneKvStore, ZoneLease};
use crate::job_lifecycle::timeout_reporter::ExecutionTimeoutReport;
use crate::observability::logger::Logger;
use futures_util::{stream, StreamExt};
use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, RwLock};
use tokio::time::{Duration, Instant};
use tokio_util::sync::CancellationToken;

// ============================================================================
// 📂 MODULE: workerpool/lease_watchdog.rs - WATCHDOG LEASE CỦA JOB
// ============================================================================
// Registry theo dõi deadline và renew fenced lease trên NATS KV mỗi 10 giây.
#[derive(Clone)]
pub struct TrackedJobExecution {
    pub started_at: Instant,
    pub max_execution_limit: Duration,
    pub abort_handle: tokio::task::AbortHandle,
    pub job_id: String,
    pub job_version: u32,
    pub attempt: u32,
    pub job_topic: String,
    pub source_domain: String,
    pub trace_id: String,
    pub lease: ZoneLease,
    pub kafka_delivery: Option<KafkaDelivery>,
}

/// Registry luồng an toàn (Thread-Safe) lưu trữ các lock key đang hoạt động
pub struct JobExecutionLeaseRegistry {
    locks: RwLock<HashMap<Arc<str>, RegisteredLock>>,
    next_registration_id: AtomicU64,
}

#[derive(Clone)]
struct RegisteredLock {
    registration_id: u64,
    info: Arc<TrackedJobExecution>,
}

impl JobExecutionLeaseRegistry {
    /// Khởi tạo Registry rỗng
    pub fn new() -> Self {
        Self {
            locks: RwLock::new(HashMap::new()),
            next_registration_id: AtomicU64::new(1),
        }
    }

    /// Register one executing job and its fenced lease/abort boundary.
    #[allow(clippy::too_many_arguments)] // [COMMENT]: Giữ metadata đăng ký hiện rõ tại callsite, không giấu trong builder/helper.
    pub fn register_job_execution(
        &self,
        lock_key: String,
        started_at: Instant,
        max_execution_limit: Duration,
        abort_handle: tokio::task::AbortHandle,
        job_id: String,
        job_version: u32,
        attempt: u32,
        job_topic: String,
        source_domain: String,
        trace_id: String,
        lease: ZoneLease,
        kafka_delivery: Option<KafkaDelivery>,
    ) -> Result<u64, &'static str> {
        let registration_id = self.next_registration_id.fetch_add(1, Ordering::Relaxed);
        match self.locks.write() {
            Ok(mut locks) => {
                // A duplicate key means the previous execution has not completed
                // its cleanup boundary. Overwriting it would let the old guard
                // remove or abort the newer execution.
                if locks.contains_key(lock_key.as_str()) {
                    return Err("JOB_EXECUTION_ALREADY_TRACKED");
                }
                locks.insert(
                    Arc::from(lock_key),
                    RegisteredLock {
                        registration_id,
                        info: Arc::new(TrackedJobExecution {
                            started_at,
                            max_execution_limit,
                            abort_handle,
                            job_id,
                            job_version,
                            attempt,
                            job_topic,
                            source_domain,
                            trace_id,
                            lease,
                            kafka_delivery,
                        }),
                    },
                );
                Ok(registration_id)
            }
            Err(_) => {
                Logger::sys_error(
                    "job_execution_lease.registry",
                    "Job execution lease registry was poisoned during register",
                    "RWLOCK_POISONED",
                );
                Err("JOB_EXECUTION_LEASE_REGISTRY_POISONED")
            }
        }
    }

    /// Remove only the same registration observed by the caller.
    pub fn remove_if_current(&self, lock_key: &str, registration_id: u64) -> bool {
        match self.locks.write() {
            Ok(mut locks) => {
                if locks
                    .get(lock_key)
                    .is_some_and(|entry| entry.registration_id == registration_id)
                {
                    locks.remove(lock_key);
                    true
                } else {
                    false
                }
            }
            Err(_) => {
                Logger::sys_error(
                    "job_execution_lease.registry",
                    "Job execution lease registry was poisoned during conditional removal",
                    "RWLOCK_POISONED",
                );
                false
            }
        }
    }

    /// Lấy bản sao danh sách để kiểm tra và gia hạn hàng loạt
    pub fn snapshot_tracked_executions(&self) -> Vec<(Arc<str>, u64, Arc<TrackedJobExecution>)> {
        match self.locks.read() {
            Ok(locks) => locks
                .iter()
                .map(|(key, entry)| (key.clone(), entry.registration_id, entry.info.clone()))
                .collect(),
            Err(_) => {
                Logger::sys_error(
                    "job_execution_lease.registry",
                    "Job execution lease registry was poisoned while taking a watchdog snapshot",
                    "RWLOCK_POISONED",
                );
                Vec::new()
            }
        }
    }
}

/// Khởi chạy vòng lặp ngầm Watchdog kiểm soát chất lượng thực thi và gia hạn khóa
pub async fn run_job_execution_lease_watchdog(
    registry: Arc<JobExecutionLeaseRegistry>,
    zone_kv: Arc<ZoneKvStore>,
    ttl_secs: u64,
    interval_duration: Duration,
    shutdown: CancellationToken,
    timeout_report_tx: tokio::sync::mpsc::Sender<ExecutionTimeoutReport>,
) {
    let zone_id = crate::config::Config::get_global().zone_id.clone();
    Logger::sys_info(
        "job_execution_lease.watchdog",
        "Watchdog & Lock Lease Auto-Renewal background loop has been started",
    );

    loop {
        tokio::select! {
            _ = shutdown.cancelled() => {
                Logger::sys_info(
                    "job_execution_lease.watchdog",
                    "Watchdog stopped after receiving the Dataplane shutdown signal",
                );
                return;
            }
            _ = tokio::time::sleep(interval_duration) => {}
        }

        let tracked_executions = registry.snapshot_tracked_executions();
        crate::observability::metrics::WorkerControlMetrics::record_watchdog_scan(
            &zone_id,
            tracked_executions.len(),
        );
        if tracked_executions.is_empty() {
            continue;
        }

        let now = Instant::now();
        let mut renewals = Vec::with_capacity(tracked_executions.len());

        for (lock_key, registration_id, info) in tracked_executions {
            let elapsed = now.duration_since(info.started_at);
            if elapsed >= info.max_execution_limit {
                // Tác vụ đã vượt quá hạn mức tối đa cho phép chạy (TIMEOUT)
                Logger::sys_warn(
                    "job_execution_lease.watchdog",
                    &format!(
                        "CRITICAL: Job {} exceeded max execution limit ({:?}). Forcing cancellation...",
                        info.job_id, info.max_execution_limit
                    ),
                    "JOB_EXECUTION_TIMEOUT_FORCED_KILL",
                );

                // The snapshot may already be obsolete. Only the registration
                // that is still current is allowed to abort or report timeout.
                if !registry.remove_if_current(&lock_key, registration_id) {
                    continue;
                }
                let info = Arc::unwrap_or_clone(info);
                info.abort_handle.abort();
                crate::observability::metrics::WorkerControlMetrics::record_watchdog_event(
                    &zone_id,
                    "execution_timeout",
                );

                let timeout_report = ExecutionTimeoutReport {
                    result: crate::job_lifecycle::result::JobExecutionResult {
                        job_id: info.job_id,
                        job_version: info.job_version,
                        attempt: info.attempt,
                        result_status: "FAILED".to_string(),
                        error_code: Some("EXECUTION_TIMEOUT".to_string()),
                        message: "Job execution aborted by watchdog due to timeout".to_string(),
                        job_topic: info.job_topic,
                        source_domain: info.source_domain,
                        trace_id: info.trace_id,
                    },
                    kafka_delivery: info.kafka_delivery,
                };
                if let Err(error) = timeout_report_tx.try_send(timeout_report) {
                    // Never block lease renewal behind Kafka reporting. An
                    // unqueued timeout keeps its source offset unsettled, so
                    // Kafka replay remains the recovery boundary.
                    Logger::sys_error(
                        "job_execution_lease.watchdog",
                        &format!(
                            "Execution timeout report queue unavailable; source remains unsettled: {error}"
                        ),
                        "WATCHDOG_TIMEOUT_REPORT_QUEUE_UNAVAILABLE",
                    );
                }
            } else {
                renewals.push((lock_key, registration_id, info));
            }
        }

        // [COMMENT]: NATS KV không có pipeline như Redis; bounded concurrency tránh cộng dồn một RTT cho từng active job.
        stream::iter(renewals)
            .for_each_concurrent(
                32,
                |(lock_key, registration_id, info)| {
                let zone_kv = zone_kv.clone();
                let registry = registry.clone();
                let zone_id = zone_id.clone();
                async move {
                    match zone_kv
                        .renew_lease(&info.lease, Duration::from_secs(ttl_secs))
                        .await
                    {
                        Ok(true) => {}
                        Ok(false) => {
                            if !registry.remove_if_current(&lock_key, registration_id) {
                                return;
                            }
                            info.abort_handle.abort();
                            crate::observability::metrics::WorkerControlMetrics::record_watchdog_event(
                                &zone_id,
                                "lease_lost",
                            );
                            Logger::sys_error(
                                "job_execution_lease.watchdog",
                                &format!(
                                    "Lost fenced Zone KV lease for job {}; task aborted",
                                    info.job_id
                                ),
                                "ZONE_KV_LEASE_LOST",
                            );
                        }
                        Err(error) => {
                            if !registry.remove_if_current(&lock_key, registration_id) {
                                return;
                            }
                            info.abort_handle.abort();
                            crate::observability::metrics::WorkerControlMetrics::record_watchdog_event(
                                &zone_id,
                                "lease_renew_error",
                            );
                            Logger::sys_error(
                                "job_execution_lease.watchdog",
                                &format!(
                                    "Could not renew fenced Zone KV lease for job {}: {error}; task aborted",
                                    info.job_id
                                ),
                                "ZONE_KV_LEASE_RENEW_FAILED",
                            );
                        }
                    }
                }
            },
            )
            .await;
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn registry_preserves_fenced_lease_and_deregisters() {
        let registry = Arc::new(JobExecutionLeaseRegistry::new());
        let task_handle = tokio::spawn(async {
            tokio::time::sleep(Duration::from_secs(10)).await;
            "finished"
        });
        let registration_id = registry
            .register_job_execution(
                "lease.job.test".to_string(),
                Instant::now(),
                Duration::from_secs(1),
                task_handle.abort_handle(),
                "test_abort_id".to_string(),
                1,
                0,
                "test_topic".to_string(),
                "TEST".to_string(),
                "test_trace_id".to_string(),
                ZoneLease {
                    key: "lease.job.test".to_string(),
                    owner_id: "pod-a".to_string(),
                    fencing_token: 7,
                },
                None,
            )
            .expect("register active lock");
        let active = registry.snapshot_tracked_executions();
        assert_eq!(active.len(), 1);
        assert_eq!(active[0].2.lease.fencing_token, 7);
        assert!(registry.remove_if_current("lease.job.test", registration_id));
        assert!(registry.snapshot_tracked_executions().is_empty());
        task_handle.abort();
    }

    #[tokio::test]
    async fn stale_registration_cannot_remove_new_execution() {
        let registry = JobExecutionLeaseRegistry::new();
        let old_task = tokio::spawn(std::future::pending::<()>());
        let old_registration = registry
            .register_job_execution(
                "lease.job.test".to_string(),
                Instant::now(),
                Duration::from_secs(1),
                old_task.abort_handle(),
                "old".to_string(),
                1,
                0,
                "test_topic".to_string(),
                "TEST".to_string(),
                "trace-old".to_string(),
                ZoneLease {
                    key: "lease.job.test".to_string(),
                    owner_id: "pod-a".to_string(),
                    fencing_token: 7,
                },
                None,
            )
            .expect("register old execution");

        let duplicate_task = tokio::spawn(std::future::pending::<()>());
        assert_eq!(
            registry.register_job_execution(
                "lease.job.test".to_string(),
                Instant::now(),
                Duration::from_secs(1),
                duplicate_task.abort_handle(),
                "new".to_string(),
                1,
                0,
                "test_topic".to_string(),
                "TEST".to_string(),
                "trace-new".to_string(),
                ZoneLease {
                    key: "lease.job.test".to_string(),
                    owner_id: "pod-a".to_string(),
                    fencing_token: 8,
                },
                None,
            ),
            Err("JOB_EXECUTION_ALREADY_TRACKED")
        );
        assert!(!registry.remove_if_current("lease.job.test", old_registration + 1));
        assert_eq!(registry.snapshot_tracked_executions().len(), 1);

        old_task.abort();
        duplicate_task.abort();
    }
}
