use std::collections::{HashMap, VecDeque};
use std::sync::atomic::{AtomicU64, AtomicU8, Ordering};
use std::sync::{Arc, Mutex, RwLock};

use futures_util::{stream, StreamExt};
use tokio::sync::mpsc::error::TrySendError;
use tokio::time::{Duration, Instant};
use tokio_util::sync::CancellationToken;

use crate::infra::kafka::KafkaDelivery;
use crate::infra::zone_kv::{ZoneKvStore, ZoneLease};
use crate::job_runtime::completion::{CompletionRequest, JobExecutionResult};
use crate::job_runtime::model::ValidatedJob;
use crate::observability::logger::Logger;

const PHASE_PREPARING: u8 = 0;
const PHASE_EXECUTING: u8 = 1;
const PHASE_COMPLETING: u8 = 2;
const MAX_CONCURRENT_LEASE_RENEWALS: usize = 128;
const MAX_LEASE_RENEW_DURATION: Duration = Duration::from_secs(3);
const COMPLETION_RETRY_INTERVAL: Duration = Duration::from_millis(100);

struct ExecutionDeadline {
    started_at: Mutex<Option<Instant>>,
    phase: AtomicU8,
    limit: Duration,
}

impl ExecutionDeadline {
    fn preparing(limit: Duration) -> Self {
        Self {
            started_at: Mutex::new(None),
            phase: AtomicU8::new(PHASE_PREPARING),
            limit,
        }
    }

    fn begin_execution(&self) -> bool {
        let Ok(mut started_at) = self.started_at.lock() else {
            return false;
        };
        *started_at = Some(Instant::now());
        // Publish the deadline before exposing Executing to the watchdog.
        self.phase.store(PHASE_EXECUTING, Ordering::Release);
        true
    }

    fn begin_completion(&self) {
        self.phase.store(PHASE_COMPLETING, Ordering::Release);
    }

    fn is_executing(&self) -> bool {
        self.phase.load(Ordering::Acquire) == PHASE_EXECUTING
    }

    fn timed_out(&self, now: Instant) -> bool {
        if !self.is_executing() {
            return false;
        }
        match self.started_at.lock() {
            Ok(started_at) => started_at
                .as_ref()
                .is_some_and(|started_at| now.duration_since(*started_at) >= self.limit),
            Err(_) => {
                // Poison means the phase invariant is no longer trustworthy;
                // fail closed by fencing and replaying the source.
                true
            }
        }
    }
}

pub struct TrackedJobExecution {
    deadline: ExecutionDeadline,
    pub cancellation: CancellationToken,
    pub job: Arc<ValidatedJob>,
    pub lease: ZoneLease,
    pub delivery: KafkaDelivery,
}

impl TrackedJobExecution {
    pub fn preparing(
        max_execution_limit: Duration,
        cancellation: CancellationToken,
        job: Arc<ValidatedJob>,
        lease: ZoneLease,
        delivery: KafkaDelivery,
    ) -> Self {
        Self {
            deadline: ExecutionDeadline::preparing(max_execution_limit),
            cancellation,
            job,
            lease,
            delivery,
        }
    }

    fn execution_timed_out(&self, now: Instant) -> bool {
        self.deadline.timed_out(now)
    }
}

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
    pub fn new() -> Self {
        Self {
            locks: RwLock::new(HashMap::new()),
            next_registration_id: AtomicU64::new(1),
        }
    }

    pub fn register_job_execution(
        &self,
        lock_key: String,
        info: TrackedJobExecution,
    ) -> Result<u64, &'static str> {
        let registration_id = self.next_registration_id.fetch_add(1, Ordering::Relaxed);
        match self.locks.write() {
            Ok(mut locks) => {
                // Overwriting a live key would allow the old cleanup guard to
                // remove or cancel a newer fenced execution.
                if locks.contains_key(lock_key.as_str()) {
                    return Err("JOB_EXECUTION_ALREADY_TRACKED");
                }
                locks.insert(
                    Arc::from(lock_key),
                    RegisteredLock {
                        registration_id,
                        info: Arc::new(info),
                    },
                );
                Ok(registration_id)
            }
            Err(_) => {
                Logger::sys_error(
                    "job.coordination.registry",
                    "Job execution registry was poisoned during register",
                    "JOB_EXECUTION_REGISTRY_POISONED",
                );
                Err("JOB_EXECUTION_REGISTRY_POISONED")
            }
        }
    }

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
                    "job.coordination.registry",
                    "Job execution registry was poisoned during removal",
                    "JOB_EXECUTION_REGISTRY_POISONED",
                );
                false
            }
        }
    }

    pub fn cancel_all_for_process_restart(&self) -> usize {
        match self.locks.write() {
            Ok(mut locks) => {
                let executions: Vec<_> = locks
                    .drain()
                    .map(|(_, entry)| entry.info.cancellation.clone())
                    .collect();
                let count = executions.len();
                // Drop the registry lock before waking executor tasks; their
                // cleanup guards immediately attempt generation-safe removal.
                drop(locks);
                for cancellation in executions {
                    cancellation.cancel();
                }
                count
            }
            Err(_) => {
                Logger::sys_error(
                    "job.coordination.registry",
                    "Could not cancel active jobs after critical task failure",
                    "JOB_EXECUTION_REGISTRY_POISONED",
                );
                0
            }
        }
    }

    pub fn mark_completion_phase(&self, lock_key: &str, registration_id: u64) -> bool {
        match self.locks.read() {
            Ok(locks) => {
                let Some(entry) = locks.get(lock_key) else {
                    return false;
                };
                if entry.registration_id != registration_id {
                    return false;
                }
                // Completion still needs lease renewal, but an already-finished
                // external executor must not race a watchdog timeout result.
                entry.info.deadline.begin_completion();
                true
            }
            Err(_) => {
                Logger::sys_error(
                    "job.coordination.registry",
                    "Job execution registry was poisoned during phase transition",
                    "JOB_EXECUTION_REGISTRY_POISONED",
                );
                false
            }
        }
    }

    pub fn mark_execution_phase(&self, lock_key: &str, registration_id: u64) -> bool {
        match self.locks.read() {
            Ok(locks) => {
                let Some(entry) = locks.get(lock_key) else {
                    return false;
                };
                if entry.registration_id != registration_id {
                    return false;
                }
                if !entry.info.deadline.begin_execution() {
                    Logger::sys_error(
                        "job.coordination.registry",
                        "Execution deadline state was poisoned during phase transition",
                        "JOB_EXECUTION_REGISTRY_POISONED",
                    );
                    return false;
                }
                true
            }
            Err(_) => {
                Logger::sys_error(
                    "job.coordination.registry",
                    "Job execution registry was poisoned during phase transition",
                    "JOB_EXECUTION_REGISTRY_POISONED",
                );
                false
            }
        }
    }

    fn remove_timed_out_if_current(&self, lock_key: &str, registration_id: u64) -> bool {
        match self.locks.write() {
            Ok(mut locks) => {
                let should_remove = locks.get(lock_key).is_some_and(|entry| {
                    entry.registration_id == registration_id && entry.info.deadline.is_executing()
                });
                if should_remove {
                    locks.remove(lock_key);
                }
                should_remove
            }
            Err(_) => {
                Logger::sys_error(
                    "job.coordination.registry",
                    "Job execution registry was poisoned during timeout fencing",
                    "JOB_EXECUTION_REGISTRY_POISONED",
                );
                false
            }
        }
    }

    pub fn snapshot(&self) -> Vec<(Arc<str>, u64, Arc<TrackedJobExecution>)> {
        match self.locks.read() {
            Ok(locks) => locks
                .iter()
                .map(|(key, entry)| (key.clone(), entry.registration_id, entry.info.clone()))
                .collect(),
            Err(_) => {
                Logger::sys_error(
                    "job.coordination.registry",
                    "Job execution registry was poisoned during watchdog snapshot",
                    "JOB_EXECUTION_REGISTRY_POISONED",
                );
                Vec::new()
            }
        }
    }
}

fn flush_timeout_completions(
    completion_tx: &tokio::sync::mpsc::Sender<CompletionRequest>,
    pending: &mut VecDeque<CompletionRequest>,
) -> bool {
    while let Some(request) = pending.pop_front() {
        match completion_tx.try_send(request) {
            Ok(()) => {}
            Err(TrySendError::Full(request)) => {
                pending.push_front(request);
                return true;
            }
            Err(TrySendError::Closed(request)) => {
                pending.push_front(request);
                return false;
            }
        }
    }
    true
}

pub async fn run_execution_watchdog(
    registry: Arc<JobExecutionLeaseRegistry>,
    zone_kv: Arc<ZoneKvStore>,
    ttl_secs: u64,
    interval_duration: Duration,
    shutdown: CancellationToken,
    completion_tx: tokio::sync::mpsc::Sender<CompletionRequest>,
    max_pending_completions: usize,
) {
    let zone_id = crate::config::Config::get_global().zone_id.clone();
    let mut shutdown_requested = false;
    let mut pending_completions = VecDeque::new();
    let mut completion_reporter_closed_logged = false;
    let scan_interval = interval_duration.max(Duration::from_millis(100));
    let mut next_scan = Instant::now();
    Logger::sys_info(
        "job.coordination.watchdog",
        "Execution deadline and fenced lease watchdog started",
    );

    loop {
        let completion_channel_open =
            flush_timeout_completions(&completion_tx, &mut pending_completions);
        crate::observability::metrics::WorkerControlMetrics::record_watchdog_completion_queue_depth(
            &zone_id,
            pending_completions.len(),
        );
        if !completion_channel_open && !completion_reporter_closed_logged {
            completion_reporter_closed_logged = true;
            Logger::sys_error(
                "job.coordination.watchdog",
                "Completion reporter is unavailable; watchdog is retaining timeout reports and sources remain unsettled",
                "WATCHDOG_COMPLETION_REPORTER_CLOSED",
            );
        }
        if shutdown_requested && !completion_channel_open {
            // The critical-task supervisor is already terminating the process.
            // Dropping in-memory reports is safe because their Kafka sources
            // remain uncommitted and will replay after restart.
            pending_completions.clear();
        }

        let now = Instant::now();
        if now < next_scan {
            let until_scan = next_scan.saturating_duration_since(now);
            let wait_duration = if pending_completions.is_empty() {
                until_scan
            } else {
                until_scan.min(COMPLETION_RETRY_INTERVAL)
            };
            if shutdown_requested {
                tokio::time::sleep(wait_duration).await;
            } else {
                tokio::select! {
                    _ = shutdown.cancelled() => {
                        // Existing executions still need renewal and deadline
                        // enforcement while worker slots gracefully drain.
                        shutdown_requested = true;
                    }
                    _ = tokio::time::sleep(wait_duration) => {}
                }
            }
            continue;
        }
        // The scan duration consumes the interval instead of adding drift after it.
        next_scan = now + scan_interval;

        let tracked = registry.snapshot();
        if shutdown_requested && tracked.is_empty() && pending_completions.is_empty() {
            Logger::sys_info(
                "job.coordination.watchdog",
                "Execution watchdog drained all registered jobs and stopped",
            );
            return;
        }
        crate::observability::metrics::WorkerControlMetrics::record_watchdog_scan(
            &zone_id,
            tracked.len(),
        );
        if tracked.is_empty() {
            continue;
        }

        let now = Instant::now();
        let mut renewals = Vec::with_capacity(tracked.len());
        for (lock_key, registration_id, info) in tracked {
            if info.execution_timed_out(now) {
                // Only the registration still current may cancel or report. A
                // stale snapshot must never affect a newer execution.
                if !registry.remove_timed_out_if_current(&lock_key, registration_id) {
                    continue;
                }
                info.cancellation.cancel();
                crate::observability::metrics::WorkerControlMetrics::record_watchdog_event(
                    &zone_id,
                    "execution_timeout",
                );
                let request = CompletionRequest {
                    job: info.job.clone(),
                    delivery: info.delivery.clone(),
                    result: JobExecutionResult::timeout(&info.job),
                };
                match completion_tx.try_send(request) {
                    Ok(()) => {}
                    Err(TrySendError::Full(request))
                        if pending_completions.len() < max_pending_completions =>
                    {
                        // Renewal must not wait behind Kafka result latency, but
                        // a full reporter queue is not permission to lose the
                        // terminal record. Keep bounded ownership and retry.
                        pending_completions.push_back(request);
                    }
                    Err(TrySendError::Full(_)) => {
                        crate::observability::metrics::WorkerControlMetrics::record_watchdog_event(
                            &zone_id,
                            "completion_overflow",
                        );
                        Logger::sys_error(
                            "job.coordination.watchdog",
                            &format!(
                                "Timeout completion overflow for job {}; source remains unsettled for replay",
                                info.job.job_id
                            ),
                            "WATCHDOG_COMPLETION_QUEUE_OVERFLOW",
                        );
                    }
                    Err(TrySendError::Closed(request))
                        if pending_completions.len() < max_pending_completions =>
                    {
                        pending_completions.push_back(request);
                    }
                    Err(TrySendError::Closed(_)) => {
                        Logger::sys_error(
                            "job.coordination.watchdog",
                            "Timeout completion reporter is closed and retention is full; source remains unsettled for replay",
                            "WATCHDOG_COMPLETION_REPORTER_CLOSED",
                        );
                    }
                }
            } else {
                renewals.push((lock_key, registration_id, info));
            }
        }

        // JetStream KV has no pipeline. Bounded concurrency avoids one RTT per
        // active job without creating an unbounded renewal storm.
        let renew_timeout = MAX_LEASE_RENEW_DURATION.min(
            Duration::from_secs(ttl_secs)
                .checked_div(3)
                .unwrap_or(MAX_LEASE_RENEW_DURATION),
        );
        stream::iter(renewals)
            .for_each_concurrent(
                MAX_CONCURRENT_LEASE_RENEWALS,
                |(lock_key, registration_id, info)| {
                let zone_kv = zone_kv.clone();
                let registry = registry.clone();
                let zone_id = zone_id.clone();
                async move {
                    match tokio::time::timeout(
                        renew_timeout,
                        zone_kv.renew_lease(&info.lease, Duration::from_secs(ttl_secs)),
                    )
                    .await
                    {
                        Ok(Ok(true)) => {}
                        Ok(Ok(false)) => {
                            if registry.remove_if_current(&lock_key, registration_id) {
                                info.cancellation.cancel();
                                crate::observability::metrics::WorkerControlMetrics::record_watchdog_event(
                                    &zone_id,
                                    "lease_lost",
                                );
                                Logger::sys_error(
                                    "job.coordination.watchdog",
                                    &format!(
                                        "Lost fenced Zone KV lease for job {}; execution cancelled and source left unsettled",
                                        info.job.job_id
                                    ),
                                    "ZONE_KV_LEASE_LOST",
                                );
                            }
                        }
                        Ok(Err(error)) => {
                            if registry.remove_if_current(&lock_key, registration_id) {
                                // Continuing after an unknown renewal result could
                                // cross the fencing boundary and duplicate effects.
                                info.cancellation.cancel();
                                crate::observability::metrics::WorkerControlMetrics::record_watchdog_event(
                                    &zone_id,
                                    "lease_renew_error",
                                );
                                Logger::sys_error(
                                    "job.coordination.watchdog",
                                    &format!(
                                        "Could not renew fenced lease for job {}: {error}; execution cancelled",
                                        info.job.job_id
                                    ),
                                    "ZONE_KV_LEASE_RENEW_FAILED",
                                );
                            }
                        }
                        Err(_) => {
                            if registry.remove_if_current(&lock_key, registration_id) {
                                // An unknown CAS outcome cannot safely extend
                                // execution beyond the current fencing window.
                                info.cancellation.cancel();
                                crate::observability::metrics::WorkerControlMetrics::record_watchdog_event(
                                    &zone_id,
                                    "lease_renew_timeout",
                                );
                                Logger::sys_error(
                                    "job.coordination.watchdog",
                                    &format!(
                                        "Fenced lease renewal timed out for job {}; execution cancelled",
                                        info.job.job_id
                                    ),
                                    "ZONE_KV_LEASE_RENEW_TIMEOUT",
                                );
                            }
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
    use super::ExecutionDeadline;
    use tokio::time::{Duration, Instant};

    #[test]
    fn preparation_and_completion_are_not_execution_timeouts() {
        let deadline = ExecutionDeadline::preparing(Duration::from_secs(1));
        assert!(!deadline.timed_out(Instant::now() + Duration::from_secs(10)));

        assert!(deadline.begin_execution());
        assert!(deadline.timed_out(Instant::now() + Duration::from_secs(2)));

        deadline.begin_completion();
        assert!(!deadline.timed_out(Instant::now() + Duration::from_secs(10)));
    }
}
