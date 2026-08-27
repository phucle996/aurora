use std::collections::{HashMap, VecDeque};
use std::sync::atomic::{AtomicBool, AtomicU64, AtomicU8, Ordering};
use std::sync::{Arc, Mutex, RwLock};

use futures_util::{stream, StreamExt};
use tokio::sync::mpsc::error::TrySendError;
use tokio::time::{Duration, Instant};
use tokio_util::sync::CancellationToken;

use crate::infra::kafka::KafkaDelivery;
use crate::infra::zone_kv::{ZoneKvStore, ZoneLease};
use crate::job_runtime::completion::{build_retry_request, RetryRequest};
use crate::job_runtime::model::{QueuedJob, ValidatedJob};
use crate::observability::logger::Logger;

const PHASE_PREPARING: u8 = 0;
const PHASE_EXECUTING: u8 = 1;
const PHASE_COMPLETING: u8 = 2;
const MAX_CONCURRENT_LEASE_RENEWALS: usize = 128;
const MAX_LEASE_RENEW_DURATION: Duration = Duration::from_secs(3);
const RECOVERY_QUEUE_RETRY_INTERVAL: Duration = Duration::from_millis(100);

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
    restart_requested: AtomicBool,
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
            restart_requested: AtomicBool::new(false),
            locks: RwLock::new(HashMap::new()),
            next_registration_id: AtomicU64::new(1),
        }
    }

    pub fn request_process_restart(&self) {
        self.restart_requested.store(true, Ordering::Release);
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

// Non-blocking flushing is required for correctness: recovery backpressure must
// never stop the same watchdog from renewing other executions' leases.
fn flush_timeout_retries(
    retry_tx: &tokio::sync::mpsc::Sender<RetryRequest>,
    pending: &mut VecDeque<RetryRequest>,
) -> bool {
    while let Some(request) = pending.pop_front() {
        match retry_tx.try_send(request) {
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
    retry_tx: tokio::sync::mpsc::Sender<RetryRequest>,
    max_pending_retries: usize,
) {
    let zone_id = crate::config::Config::get_global().zone_id.clone();
    let mut shutdown_requested = false;
    let mut pending_retries = VecDeque::new();
    let mut retry_scheduler_closed_logged = false;
    let scan_interval = interval_duration.max(Duration::from_millis(100));
    let mut next_scan = Instant::now();
    Logger::sys_info(
        "job.coordination.watchdog",
        "Execution deadline and fenced lease watchdog started",
    );

    loop {
        if registry.restart_requested.load(Ordering::Acquire) {
            // This is a supervised critical task. Returning makes the process
            // restart instead of leaving an uncommitted offset stranded.
            return;
        }
        let retry_channel_open = flush_timeout_retries(&retry_tx, &mut pending_retries);
        crate::observability::metrics::WorkerControlMetrics::record_watchdog_recovery_queue_depth(
            &zone_id,
            pending_retries.len(),
        );
        if !retry_channel_open && !retry_scheduler_closed_logged {
            retry_scheduler_closed_logged = true;
            Logger::sys_error(
                "job.coordination.watchdog",
                "Retry scheduler is unavailable; watchdog is retaining timeout recovery and sources remain unsettled",
                "WATCHDOG_RETRY_SCHEDULER_CLOSED",
            );
        }
        if shutdown_requested && !retry_channel_open {
            // The critical-task supervisor is already terminating the process.
            // Dropping in-memory retries is safe because their Kafka sources
            // remain uncommitted and will replay after restart.
            pending_retries.clear();
        }

        let now = Instant::now();
        if now < next_scan {
            let until_scan = next_scan.saturating_duration_since(now);
            let wait_duration = if pending_retries.is_empty() {
                until_scan
            } else {
                until_scan.min(RECOVERY_QUEUE_RETRY_INTERVAL)
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
        if shutdown_requested && tracked.is_empty() && pending_retries.is_empty() {
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
                // A deadline cancels only the local future, not an already
                // accepted provider task. It is never evidence of failure.
                // Recover the exact command/attempt (including protected bytes
                // and delivery epoch), just like lease-contention redelivery.
                // Timeout recovery does not consume the business retry budget.
                let queued = QueuedJob {
                    job: info.job.clone(),
                    delivery: info.delivery.clone(),
                };
                let delay = Duration::from_millis(
                    ttl_secs
                        .saturating_mul(1_000)
                        .saturating_add(rand::random::<u64>() % 2_000),
                );
                let request = build_retry_request(
                    &queued,
                    info.job.attempt,
                    delay,
                    "JOB_EXECUTION_OUTCOME_UNKNOWN",
                );
                Logger::sys_warn(
                    "job.coordination.watchdog",
                    &format!("Deadline reached for job {}; replaying the same operation without a terminal result", info.job.job_id),
                    "JOB_EXECUTION_OUTCOME_UNKNOWN",
                );
                match retry_tx.try_send(request) {
                    Ok(()) => {}
                    Err(TrySendError::Full(request))
                        if pending_retries.len() < max_pending_retries =>
                    {
                        // Renewal must not wait behind Kafka retry latency.
                        // Source stays unacknowledged until retry is durable.
                        pending_retries.push_back(request);
                    }
                    Err(TrySendError::Full(_)) => {
                        crate::observability::metrics::WorkerControlMetrics::record_watchdog_event(
                            &zone_id,
                            "recovery_overflow",
                        );
                        Logger::sys_error(
                            "job.coordination.watchdog",
                            &format!(
                                "Timeout recovery overflow for job {}; source remains unsettled for replay",
                                info.job.job_id
                            ),
                            "WATCHDOG_RECOVERY_QUEUE_OVERFLOW",
                        );
                        // Exit this critical task so its supervisor restarts
                        // the process and Kafka replays the uncommitted source.
                        // Merely logging here could strand it in this assignment.
                        return;
                    }
                    Err(TrySendError::Closed(request))
                        if pending_retries.len() < max_pending_retries =>
                    {
                        pending_retries.push_back(request);
                    }
                    Err(TrySendError::Closed(_)) => {
                        Logger::sys_error(
                            "job.coordination.watchdog",
                            "Timeout retry scheduler is closed and retention is full; source remains unsettled for replay",
                            "WATCHDOG_RETRY_SCHEDULER_CLOSED",
                        );
                        return;
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
        let lost = stream::iter(renewals)
            .map(|(lock_key, registration_id, info)| {
                let zone_kv = zone_kv.clone();
                let registry = registry.clone();
                async move {
                    let renewal = tokio::time::timeout(
                        renew_timeout,
                        zone_kv.renew_lease(&info.lease, Duration::from_secs(ttl_secs)),
                    )
                    .await;
                    if matches!(renewal, Ok(Ok(true))) {
                        return None;
                    }
                    if !registry.remove_if_current(&lock_key, registration_id) {
                        return None;
                    }
                    info.cancellation.cancel();
                    Some(info)
                }
            })
            .buffer_unordered(MAX_CONCURRENT_LEASE_RENEWALS)
            .collect::<Vec<_>>()
            .await;
        for info in lost.into_iter().flatten() {
            Logger::sys_warn(
                "job.coordination.watchdog",
                &format!(
                    "Lease renewal lost for {}; replaying the fenced operation",
                    info.job.job_id
                ),
                "ZONE_KV_LEASE_RENEW_UNKNOWN",
            );
            let request = build_retry_request(
                &QueuedJob {
                    job: info.job.clone(),
                    delivery: info.delivery.clone(),
                },
                info.job.attempt,
                Duration::from_millis(
                    ttl_secs
                        .saturating_mul(1_000)
                        .saturating_add(rand::random::<u64>() % 2_000),
                ),
                "JOB_LEASE_OUTCOME_UNKNOWN",
            );
            match retry_tx.try_send(request) {
                Ok(()) => {}
                Err(TrySendError::Full(request)) if pending_retries.len() < max_pending_retries => {
                    pending_retries.push_back(request);
                }
                // No durable recovery path remains. The critical task supervisor
                // restarts the process; original Kafka offsets are not committed.
                Err(_) => return,
            }
        }
    }
}

#[cfg(test)]
#[path = "../test/watchdog.rs"]
mod tests;
