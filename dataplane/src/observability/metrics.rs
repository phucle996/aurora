use arc_swap::ArcSwap;
use opentelemetry::metrics::{Counter, Gauge, Histogram};
use opentelemetry::{global, KeyValue};
use serde::{Deserialize, Serialize};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, OnceLock};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};
use tokio::time::sleep;

use crate::infra::kafka::KafkaTransport;
use crate::infra::zone_kv::ZoneKvStore;
use crate::observability::logger::Logger;
use crate::workerpool::pool::{TaskGuard, WorkerLifecycleManager};

const SAMPLE_INTERVAL: Duration = Duration::from_secs(5);
pub const NODE_RUNTIME_SAMPLE_MAX_AGE_MS: u64 = 15_000;
const CGROUP_CPU_STAT: &str = "/sys/fs/cgroup/cpu.stat";
const CGROUP_CPU_MAX: &str = "/sys/fs/cgroup/cpu.max";
const CGROUP_MEMORY_CURRENT: &str = "/sys/fs/cgroup/memory.current";
const CGROUP_MEMORY_MAX: &str = "/sys/fs/cgroup/memory.max";

static LATEST_SAMPLE: OnceLock<ArcSwap<NodeRuntimeSample>> = OnceLock::new();
static NODE_CPU_UTILIZATION: OnceLock<Gauge<f64>> = OnceLock::new();
static NODE_MEMORY_UTILIZATION: OnceLock<Gauge<f64>> = OnceLock::new();
static NODE_CPU_THROTTLED_RATIO: OnceLock<Gauge<f64>> = OnceLock::new();
static NODE_MEMORY_WORKING_SET_BYTES: OnceLock<Gauge<f64>> = OnceLock::new();
static NODE_RUNTIME_SAMPLE_VALID: OnceLock<Gauge<f64>> = OnceLock::new();
static NODE_RUNTIME_SAMPLE_AGE_SECONDS: OnceLock<Gauge<f64>> = OnceLock::new();
static STREAM_LAG: OnceLock<Gauge<f64>> = OnceLock::new();
static ACTIVE_WORKERS: OnceLock<Gauge<f64>> = OnceLock::new();
static WORKER_SLOTS: OnceLock<Gauge<f64>> = OnceLock::new();
static ADMITTED_JOBS: OnceLock<Gauge<f64>> = OnceLock::new();
static JOB_EXECUTION_LATENCY: OnceLock<Histogram<f64>> = OnceLock::new();
static JOB_ATTEMPTS_COMPLETED: OnceLock<Counter<u64>> = OnceLock::new();
static WATCHDOG_ACTIVE_LOCKS: OnceLock<Gauge<f64>> = OnceLock::new();
static WATCHDOG_EVENTS: OnceLock<Counter<u64>> = OnceLock::new();
static WATCHDOG_RECOVERY_QUEUE_DEPTH: OnceLock<Gauge<f64>> = OnceLock::new();
static WORKER_SCALE_TARGET: OnceLock<Gauge<f64>> = OnceLock::new();
static JOB_RUNTIME_EVENTS: OnceLock<Counter<u64>> = OnceLock::new();
static JOB_RETRY_QUEUE_DEPTH: OnceLock<Gauge<f64>> = OnceLock::new();
static KAFKA_UNSETTLED_RECORDS: OnceLock<Gauge<f64>> = OnceLock::new();

/// One coherent observation fan-outs to admission control, OTel and Zone KV.
///
/// `sample_valid` is deliberately separate from the numeric values: a failed
/// cgroup read must never look like a fresh zero-load sample to Zone Control.
#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(default)]
pub struct NodeRuntimeSample {
    pub sequence: u64,
    pub cpu: f64,
    pub ram: f64,
    pub cpu_throttled_ratio: f64,
    pub memory_working_set_bytes: u64,
    pub active_workers: usize,
    pub starting_workers: usize,
    pub ready_workers: usize,
    pub draining_workers: usize,
    pub admitted_jobs: usize,
    pub job_queue_lag: u64,
    pub job_queue_lag_stale: bool,
    pub job_queue_lag_observed_at_unix_ms: u64,
    pub updated_at: u64,
    pub sample_observed_at_unix_ms: u64,
    pub sample_valid: bool,
    pub loaded_payload_keys: Vec<NodePayloadKeyReadiness>,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
pub struct NodePayloadKeyReadiness {
    pub key_id: String,
    pub public_key_fingerprint: Vec<u8>,
}

impl NodeRuntimeSample {
    pub fn is_fresh(&self, now_ms: u64) -> bool {
        self.sample_valid
            && self.sample_observed_at_unix_ms > 0
            && now_ms
                .checked_sub(self.sample_observed_at_unix_ms)
                .is_some_and(|age| age <= NODE_RUNTIME_SAMPLE_MAX_AGE_MS)
    }
}

fn latest_store() -> &'static ArcSwap<NodeRuntimeSample> {
    LATEST_SAMPLE.get_or_init(|| ArcSwap::from_pointee(NodeRuntimeSample::default()))
}

pub fn latest_sample() -> Arc<NodeRuntimeSample> {
    latest_store().load_full()
}

fn current_unix_time() -> (u64, u64) {
    let duration = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default();
    (
        duration.as_secs(),
        duration
            .as_millis()
            .min(u64::MAX as u128)
            .try_into()
            .unwrap_or(u64::MAX),
    )
}

/// Local admission reads the same sample that gets exported and reported.
/// Stale/invalid telemetry fails closed instead of treating an unreadable cgroup
/// as zero load.
pub fn cpu_usage() -> f64 {
    let (now_seconds, now_ms) = current_unix_time();
    let sample = latest_sample();
    // There is no previous interval during the first probe.  Treat that
    // bootstrap window as zero load; subsequent invalid/stale samples fail
    // closed so admission cannot overload a node whose cgroup is unreadable.
    if sample.sequence <= 1 {
        return 0.0;
    }
    if sample.updated_at <= now_seconds && sample.is_fresh(now_ms) {
        sample.cpu
    } else {
        1.0
    }
}

pub fn ram_usage() -> f64 {
    let (_, now_ms) = current_unix_time();
    let sample = latest_sample();
    if sample.sequence <= 1 {
        return 0.0;
    }
    if sample.is_fresh(now_ms) {
        sample.ram
    } else {
        1.0
    }
}

pub struct NodeRuntimeSampler;

impl NodeRuntimeSampler {
    pub fn cpu_usage() -> f64 {
        cpu_usage()
    }

    pub fn ram_usage() -> f64 {
        ram_usage()
    }
}

impl NodeRuntimeSampler {
    /// Start one sampler per Dataplane process. The sampler owns measurement;
    /// all consumers read the immutable snapshot instead of probing `/proc`.
    pub fn start_node_runtime_sampler(
        node_id: String,
        zone_kv: Arc<ZoneKvStore>,
        worker_pool: Arc<WorkerLifecycleManager>,
        kafka: Arc<KafkaTransport>,
        admitted_jobs: Arc<AtomicUsize>,
        payload_keyring: Arc<crate::security::jobpayload::PayloadKeyring>,
        task_guard: TaskGuard,
    ) {
        tokio::spawn(async move {
            let _task_guard = task_guard;
            let shutdown = worker_pool.cancel_token();
            Logger::sys_info(
                "metrics.node_runtime.start",
                &format!("Starting NodeRuntimeSampler for node={node_id}"),
            );
            let zone_id = crate::config::Config::get_global().zone_id.clone();

            let mut probe = RuntimeProbe::default();
            let mut sequence = 0_u64;
            loop {
                sequence = sequence.wrapping_add(1);
                let worker_states = worker_pool.worker_state_counts();
                let active_workers = worker_states.total();
                let admitted_jobs = admitted_jobs.load(Ordering::Relaxed);
                let (job_queue_lag, job_queue_lag_stale, job_queue_lag_observed_at) =
                    kafka.job_lag_snapshot();
                let (updated_at, observed_at) = current_unix_time();
                let mut sample = probe
                    .collect(
                        sequence,
                        active_workers,
                        worker_states.starting,
                        worker_states.ready,
                        worker_states.draining,
                        admitted_jobs,
                        job_queue_lag,
                        job_queue_lag_stale,
                        job_queue_lag_observed_at,
                        updated_at,
                        observed_at,
                    )
                    .await;

                // A lag snapshot can be older than the resource sample while
                // admission is paused. Preserve that distinction for Zone Control.
                sample.job_queue_lag = job_queue_lag;
                sample.job_queue_lag_stale = job_queue_lag_stale
                    || job_queue_lag_observed_at == 0
                    || observed_at
                        .checked_sub(job_queue_lag_observed_at)
                        .is_none_or(|age| age > NODE_RUNTIME_SAMPLE_MAX_AGE_MS);
                sample.job_queue_lag_observed_at_unix_ms = job_queue_lag_observed_at;
                sample.loaded_payload_keys = payload_keyring
                    .loaded_keys()
                    .iter()
                    .map(|key| NodePayloadKeyReadiness {
                        key_id: key.key_id.to_string(),
                        public_key_fingerprint: key.public_key_fingerprint.to_vec(),
                    })
                    .collect();
                latest_store().store(Arc::new(sample.clone()));

                NodeRuntimeMetrics::record_sample(&zone_id, &sample);
                Logger::record_pipeline_metrics(&zone_id);

                match serde_json::to_vec(&sample) {
                    Ok(value) => {
                        let key = format!(
                            "zone.node.{}",
                            node_id.replace(
                                |character: char| !character.is_ascii_alphanumeric()
                                    && character != '-'
                                    && character != '_',
                                "_",
                            )
                        );
                        match tokio::time::timeout(
                            Duration::from_secs(2),
                            zone_kv.health_put(&key, bytes::Bytes::from(value)),
                        )
                        .await
                        {
                            Ok(Ok(_)) => {}
                            Ok(Err(error)) => Logger::sys_warn_with_fields(
                                "metrics.node_runtime.snapshot",
                                "DATAPLANE_RESOURCE_SNAPSHOT_WRITE_FAILED",
                                "Could not write node runtime snapshot to Zone health KV",
                                &error,
                                crate::observability::logger::LogFields {
                                    operation_id: Some(&node_id),
                                    retryable: Some(true),
                                    outcome: Some("stale"),
                                    ..Default::default()
                                },
                            ),
                            Err(_) => Logger::sys_warn_with_fields(
                                "metrics.node_runtime.snapshot",
                                "DATAPLANE_RESOURCE_SNAPSHOT_WRITE_TIMEOUT",
                                "Timed out writing node runtime snapshot to Zone health KV",
                                "",
                                crate::observability::logger::LogFields {
                                    operation_id: Some(&node_id),
                                    retryable: Some(true),
                                    outcome: Some("stale"),
                                    ..Default::default()
                                },
                            ),
                        }
                    }
                    Err(error) => Logger::sys_error(
                        "metrics.node_runtime.snapshot",
                        "Could not serialize node runtime snapshot",
                        &error.to_string(),
                    ),
                }

                tokio::select! {
                    _ = shutdown.cancelled() => return,
                    _ = sleep(SAMPLE_INTERVAL) => {}
                }
            }
        });
    }
}

pub struct NodeRuntimeMetrics;

impl NodeRuntimeMetrics {
    /// Register OTel instruments once. OTel is the export/diagnostic sink;
    /// local admission and Zone Control never read values back from the
    /// Collector, they consume the same in-memory sample directly.
    pub fn init_registry() {
        let _ = node_cpu_utilization();
        let _ = node_memory_utilization();
        let _ = node_cpu_throttled_ratio();
        let _ = node_memory_working_set_bytes();
        let _ = node_runtime_sample_valid();
        let _ = node_runtime_sample_age_seconds();
        let _ = stream_lag();
        let _ = active_workers();
        let _ = worker_slots();
        let _ = admitted_jobs();
    }

    pub fn record_sample(zone_id: &str, sample: &NodeRuntimeSample) {
        let attributes = [KeyValue::new("zone_id", zone_id.to_string())];
        let now_ms = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|value| value.as_millis().min(u64::MAX as u128) as u64)
            .unwrap_or_default();
        let age_seconds = now_ms.saturating_sub(sample.sample_observed_at_unix_ms) as f64 / 1_000.0;

        node_cpu_utilization().record(sample.cpu, &attributes);
        node_memory_utilization().record(sample.ram, &attributes);
        node_cpu_throttled_ratio().record(sample.cpu_throttled_ratio, &attributes);
        node_memory_working_set_bytes().record(sample.memory_working_set_bytes as f64, &attributes);
        node_runtime_sample_valid().record(sample.sample_valid as u8 as f64, &attributes);
        node_runtime_sample_age_seconds().record(age_seconds, &attributes);
        stream_lag().record(sample.job_queue_lag as f64, &attributes);
        active_workers().record(sample.active_workers as f64, &attributes);
        for (state, value) in [
            ("starting", sample.starting_workers),
            ("ready", sample.ready_workers),
            ("draining", sample.draining_workers),
        ] {
            worker_slots().record(
                value as f64,
                &[
                    KeyValue::new("zone_id", zone_id.to_string()),
                    KeyValue::new("state", state),
                ],
            );
        }
        admitted_jobs().record(sample.admitted_jobs as f64, &attributes);
    }
}

pub struct JobExecutionMetrics;

impl JobExecutionMetrics {
    pub fn init_registry() {
        let _ = job_execution_latency();
        let _ = job_attempts_completed();
    }

    pub fn record_attempt(zone_id: &str, job_topic: &str, status: &str, latency_ms: f64) {
        // Kafka payloads can contain new topic values. Keeping only the
        // allow-listed workload family bounds metric cardinality while the full
        // topic remains available in traces and structured logs.
        let workload = match job_topic.split_once('.').map(|(workload, _)| workload) {
            Some("mail") => "mail",
            Some("vps") => "vps",
            Some("storage") => "storage",
            _ => "unknown",
        };
        let attributes = [
            KeyValue::new("zone_id", zone_id.to_string()),
            KeyValue::new("workload", workload),
            KeyValue::new("status", status.to_string()),
        ];
        job_execution_latency().record((latency_ms / 1_000.0).max(0.0), &attributes);
        job_attempts_completed().add(1, &attributes);
    }
}

pub struct WorkerControlMetrics;

impl WorkerControlMetrics {
    pub fn init_registry() {
        let _ = watchdog_active_locks();
        let _ = watchdog_events();
        let _ = watchdog_recovery_queue_depth();
        let _ = worker_scale_target();
        let _ = job_runtime_events();
        let _ = job_retry_queue_depth();
        let _ = kafka_unsettled_records();
    }

    pub fn record_watchdog_scan(zone_id: &str, active_locks: usize) {
        watchdog_active_locks().record(
            active_locks as f64,
            &[KeyValue::new("zone_id", zone_id.to_string())],
        );
    }

    pub fn record_watchdog_event(zone_id: &str, event: &'static str) {
        watchdog_events().add(
            1,
            &[
                KeyValue::new("zone_id", zone_id.to_string()),
                KeyValue::new("event", event),
            ],
        );
    }

    pub fn record_watchdog_recovery_queue_depth(zone_id: &str, depth: usize) {
        watchdog_recovery_queue_depth().record(
            depth as f64,
            &[KeyValue::new("zone_id", zone_id.to_string())],
        );
    }

    pub fn record_scale_target(zone_id: &str, source: &'static str, target: usize) {
        worker_scale_target().record(
            target as f64,
            &[
                KeyValue::new("zone_id", zone_id.to_string()),
                KeyValue::new("source", source),
            ],
        );
    }

    pub fn record_job_runtime_event(zone_id: &str, event: &'static str) {
        job_runtime_events().add(
            1,
            &[
                KeyValue::new("zone_id", zone_id.to_string()),
                KeyValue::new("event", event),
            ],
        );
    }

    pub fn record_job_retry_queue_depth(zone_id: &str, depth: usize) {
        job_retry_queue_depth().record(
            depth as f64,
            &[KeyValue::new("zone_id", zone_id.to_string())],
        );
    }

    pub fn record_kafka_unsettled_records(zone_id: &str, records: usize) {
        kafka_unsettled_records().record(
            records as f64,
            &[KeyValue::new("zone_id", zone_id.to_string())],
        );
    }
}

fn job_execution_latency() -> &'static Histogram<f64> {
    JOB_EXECUTION_LATENCY.get_or_init(|| {
        global::meter("aurora-dataplane")
            .f64_histogram("dataplane_job_execution_latency_seconds")
            .with_description("Business executor latency per completed Dataplane job attempt")
            .init()
    })
}

fn job_attempts_completed() -> &'static Counter<u64> {
    JOB_ATTEMPTS_COMPLETED.get_or_init(|| {
        global::meter("aurora-dataplane")
            .u64_counter("dataplane_job_attempts_completed_total")
            .with_description("Completed Dataplane job attempts by bounded workload and status")
            .init()
    })
}

fn watchdog_active_locks() -> &'static Gauge<f64> {
    WATCHDOG_ACTIVE_LOCKS.get_or_init(|| {
        global::meter("aurora-dataplane")
            .f64_gauge("dataplane_watchdog_active_locks")
            .with_description("Executions currently protected by the local fenced lease watchdog")
            .init()
    })
}

fn watchdog_events() -> &'static Counter<u64> {
    WATCHDOG_EVENTS.get_or_init(|| {
        global::meter("aurora-dataplane")
            .u64_counter("dataplane_watchdog_events_total")
            .with_description("Bounded watchdog timeout and lease-loss events")
            .init()
    })
}

fn watchdog_recovery_queue_depth() -> &'static Gauge<f64> {
    WATCHDOG_RECOVERY_QUEUE_DEPTH.get_or_init(|| {
        global::meter("aurora-dataplane")
            .f64_gauge("dataplane_watchdog_recovery_queue_depth")
            .with_description(
                "Unknown-outcome command replays retained while the Kafka retry scheduler is busy",
            )
            .init()
    })
}

fn worker_scale_target() -> &'static Gauge<f64> {
    WORKER_SCALE_TARGET.get_or_init(|| {
        global::meter("aurora-dataplane")
            .f64_gauge("dataplane_worker_scale_target")
            .with_description("Leader-issued or follower-applied per-node worker target")
            .init()
    })
}

fn job_runtime_events() -> &'static Counter<u64> {
    JOB_RUNTIME_EVENTS.get_or_init(|| {
        global::meter("aurora-dataplane")
            .u64_counter("dataplane_job_runtime_events_total")
            .with_description("Bounded job lease, retry scheduling and retry publication outcomes")
            .init()
    })
}

fn job_retry_queue_depth() -> &'static Gauge<f64> {
    JOB_RETRY_QUEUE_DEPTH.get_or_init(|| {
        global::meter("aurora-dataplane")
            .f64_gauge("dataplane_job_retry_queue_depth")
            .with_description("Pending delayed retries from lease contention or executor backoff")
            .init()
    })
}

fn kafka_unsettled_records() -> &'static Gauge<f64> {
    KAFKA_UNSETTLED_RECORDS.get_or_init(|| {
        global::meter("aurora-dataplane")
            .f64_gauge("dataplane_kafka_unsettled_records")
            .with_description("Fetched Kafka jobs still waiting for contiguous terminal settlement")
            .init()
    })
}

fn node_cpu_utilization() -> &'static Gauge<f64> {
    NODE_CPU_UTILIZATION.get_or_init(|| {
        global::meter("aurora-dataplane")
            .f64_gauge("dataplane_node_cpu_utilization")
            .with_description("Cgroup CPU utilization ratio for this Dataplane node")
            .init()
    })
}

fn node_memory_utilization() -> &'static Gauge<f64> {
    NODE_MEMORY_UTILIZATION.get_or_init(|| {
        global::meter("aurora-dataplane")
            .f64_gauge("dataplane_node_memory_utilization")
            .with_description("Cgroup memory utilization ratio for this Dataplane node")
            .init()
    })
}

fn node_cpu_throttled_ratio() -> &'static Gauge<f64> {
    NODE_CPU_THROTTLED_RATIO.get_or_init(|| {
        global::meter("aurora-dataplane")
            .f64_gauge("dataplane_node_cpu_throttled_ratio")
            .with_description("Ratio of cgroup CPU time spent throttled")
            .init()
    })
}

fn node_memory_working_set_bytes() -> &'static Gauge<f64> {
    NODE_MEMORY_WORKING_SET_BYTES.get_or_init(|| {
        global::meter("aurora-dataplane")
            .f64_gauge("dataplane_node_memory_working_set_bytes")
            .with_description("Cgroup memory working set in bytes")
            .init()
    })
}

fn node_runtime_sample_valid() -> &'static Gauge<f64> {
    NODE_RUNTIME_SAMPLE_VALID.get_or_init(|| {
        global::meter("aurora-dataplane")
            .f64_gauge("dataplane_node_runtime_sample_valid")
            .with_description("Whether the latest node runtime sample is valid")
            .init()
    })
}

fn node_runtime_sample_age_seconds() -> &'static Gauge<f64> {
    NODE_RUNTIME_SAMPLE_AGE_SECONDS.get_or_init(|| {
        global::meter("aurora-dataplane")
            .f64_gauge("dataplane_node_runtime_sample_age_seconds")
            .with_description("Age of the latest node runtime sample in seconds")
            .init()
    })
}

fn stream_lag() -> &'static Gauge<f64> {
    STREAM_LAG.get_or_init(|| {
        global::meter("aurora-dataplane")
            .f64_gauge("dataplane_kafka_consumer_lag")
            .with_description("Kafka consumer lag cua tung Zone")
            .init()
    })
}

fn active_workers() -> &'static Gauge<f64> {
    ACTIVE_WORKERS.get_or_init(|| {
        global::meter("aurora-dataplane")
            .f64_gauge("dataplane_active_workers")
            .with_description("Current worker slots on this Dataplane node")
            .init()
    })
}

fn worker_slots() -> &'static Gauge<f64> {
    WORKER_SLOTS.get_or_init(|| {
        global::meter("aurora-dataplane")
            .f64_gauge("dataplane_worker_slots")
            .with_description("Worker slots by bounded lifecycle state")
            .init()
    })
}

fn admitted_jobs() -> &'static Gauge<f64> {
    ADMITTED_JOBS.get_or_init(|| {
        global::meter("aurora-dataplane")
            .f64_gauge("dataplane_admitted_jobs")
            .with_description("Jobs queued or executing after local admission")
            .init()
    })
}

#[derive(Default)]
struct RuntimeProbe {
    previous_cgroup_cpu_usec: Option<u64>,
    previous_cgroup_throttled_usec: Option<u64>,
    previous_proc_active: Option<u64>,
    previous_proc_total: Option<u64>,
    previous_wall: Option<Instant>,
    previous_memory: f64,
    previous_cpu: f64,
}

impl RuntimeProbe {
    // The sample is intentionally assembled from one coherent observation
    // tuple; keeping these inputs explicit avoids hidden second probes.
    #[allow(clippy::too_many_arguments)]
    async fn collect(
        &mut self,
        sequence: u64,
        active_workers: usize,
        starting_workers: usize,
        ready_workers: usize,
        draining_workers: usize,
        admitted_jobs: usize,
        job_queue_lag: u64,
        job_queue_lag_stale: bool,
        job_queue_lag_observed_at_unix_ms: u64,
        updated_at: u64,
        observed_at: u64,
    ) -> NodeRuntimeSample {
        let (
            cgroup_cpu_stat,
            cgroup_cpu_max,
            proc_stat,
            cgroup_memory_current,
            cgroup_memory_max,
            proc_meminfo,
        ) = tokio::join!(
            tokio::fs::read_to_string(CGROUP_CPU_STAT),
            tokio::fs::read_to_string(CGROUP_CPU_MAX),
            tokio::fs::read_to_string("/proc/stat"),
            tokio::fs::read_to_string(CGROUP_MEMORY_CURRENT),
            tokio::fs::read_to_string(CGROUP_MEMORY_MAX),
            tokio::fs::read_to_string("/proc/meminfo"),
        );

        let (cpu, cpu_throttled_ratio) = self.read_cpu(
            cgroup_cpu_stat.ok().as_deref(),
            cgroup_cpu_max.ok().as_deref(),
            proc_stat.ok().as_deref(),
        );
        let (ram, memory_working_set_bytes) = read_memory(
            cgroup_memory_current.ok().as_deref(),
            cgroup_memory_max.ok().as_deref(),
            proc_meminfo.ok().as_deref(),
        );
        if let Some(value) = ram {
            self.previous_memory = value;
        }
        let sample_valid = cpu.is_some() && ram.is_some();
        NodeRuntimeSample {
            sequence,
            cpu: cpu.unwrap_or(self.previous_cpu),
            ram: ram.unwrap_or(self.previous_memory),
            cpu_throttled_ratio,
            memory_working_set_bytes,
            active_workers,
            starting_workers,
            ready_workers,
            draining_workers,
            admitted_jobs,
            job_queue_lag,
            job_queue_lag_stale,
            job_queue_lag_observed_at_unix_ms,
            updated_at,
            sample_observed_at_unix_ms: observed_at,
            sample_valid,
            loaded_payload_keys: Vec::new(),
        }
    }

    fn read_cpu(
        &mut self,
        cgroup_stat: Option<&str>,
        cgroup_max: Option<&str>,
        proc_stat: Option<&str>,
    ) -> (Option<f64>, f64) {
        let now = Instant::now();
        if let Some(usage_usec) = parse_field(cgroup_stat.unwrap_or_default(), "usage_usec") {
            let throttled_usec =
                parse_field(cgroup_stat.unwrap_or_default(), "throttled_usec").unwrap_or(0);
            let quota_cores = parse_cpu_quota(cgroup_max.unwrap_or_default());
            let (utilization, throttled_ratio) = match (
                self.previous_cgroup_cpu_usec,
                self.previous_cgroup_throttled_usec,
                self.previous_wall,
            ) {
                (Some(previous_usage), Some(previous_throttled), Some(previous_wall))
                    if now > previous_wall =>
                {
                    let elapsed_usec = now.duration_since(previous_wall).as_micros() as f64;
                    let usage_delta = usage_usec.saturating_sub(previous_usage) as f64;
                    let utilization = (usage_delta / (elapsed_usec * quota_cores)).clamp(0.0, 1.0);
                    let throttled_delta = throttled_usec.saturating_sub(previous_throttled) as f64;
                    let throttled_ratio =
                        (throttled_delta / (throttled_delta + usage_delta)).clamp(0.0, 1.0);
                    (Some(utilization), throttled_ratio)
                }
                _ => (None, 0.0),
            };
            self.previous_cgroup_cpu_usec = Some(usage_usec);
            self.previous_cgroup_throttled_usec = Some(throttled_usec);
            self.previous_wall = Some(now);
            if let Some(value) = utilization {
                self.previous_cpu = value;
            }
            return (utilization, throttled_ratio);
        }

        let Some((active, total)) = parse_proc_cpu(proc_stat.unwrap_or_default()) else {
            return (None, 0.0);
        };
        let utilization = self
            .previous_proc_active
            .zip(self.previous_proc_total)
            .and_then(|(previous_active, previous_total)| {
                let active_delta = active.saturating_sub(previous_active) as f64;
                let total_delta = total.saturating_sub(previous_total) as f64;
                (total_delta > 0.0).then(|| (active_delta / total_delta).clamp(0.0, 1.0))
            });
        self.previous_proc_active = Some(active);
        self.previous_proc_total = Some(total);
        if let Some(value) = utilization {
            self.previous_cpu = value;
        }
        (utilization, 0.0)
    }
}

fn parse_field(raw: &str, field: &str) -> Option<u64> {
    raw.lines()
        .find_map(|line| line.strip_prefix(field)?.split_whitespace().next())
        .and_then(|value| value.parse::<u64>().ok())
}

fn parse_cpu_quota(raw: &str) -> f64 {
    let mut fields = raw.split_whitespace();
    let quota = fields.next();
    let period = fields.next().and_then(|value| value.parse::<f64>().ok());
    match (quota, period) {
        (Some("max"), _) | (_, None) => std::thread::available_parallelism()
            .map(|value| value.get() as f64)
            .unwrap_or(1.0),
        (Some(value), Some(period)) => value
            .parse::<f64>()
            .ok()
            .filter(|quota| *quota > 0.0 && period > 0.0)
            .map(|quota| quota / period)
            .unwrap_or(1.0),
        _ => 1.0,
    }
}

fn parse_proc_cpu(raw: &str) -> Option<(u64, u64)> {
    let line = raw.lines().next()?;
    let mut fields = line.split_whitespace();
    (fields.next()? == "cpu").then_some(())?;
    let values = fields
        .take(10)
        .map(|value| value.parse::<u64>().ok())
        .collect::<Option<Vec<_>>>()?;
    (values.len() >= 5).then(|| {
        let idle = values[3].saturating_add(values.get(4).copied().unwrap_or_default());
        let total = values.iter().copied().sum::<u64>();
        (total.saturating_sub(idle), total)
    })
}

fn read_memory(
    cgroup_current: Option<&str>,
    cgroup_max: Option<&str>,
    proc_meminfo: Option<&str>,
) -> (Option<f64>, u64) {
    if let (Some(current), Some(maximum)) = (
        cgroup_current.and_then(|value| value.trim().parse::<u64>().ok()),
        cgroup_max
            .filter(|value| value.trim() != "max")
            .and_then(|value| value.trim().parse::<u64>().ok()),
    ) {
        if maximum > 0 {
            return (
                Some((current as f64 / maximum as f64).clamp(0.0, 1.0)),
                current,
            );
        }
    }

    let total_kib = parse_field(proc_meminfo.unwrap_or_default(), "MemTotal:");
    let available_kib = parse_field(proc_meminfo.unwrap_or_default(), "MemAvailable:");
    match (total_kib, available_kib) {
        (Some(total), Some(available)) if total > 0 => (
            Some((1.0 - (available as f64 / total as f64)).clamp(0.0, 1.0)),
            total.saturating_sub(available).saturating_mul(1024),
        ),
        _ => (None, 0),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn proc_cpu_parser_rejects_short_lines_without_panicking() {
        assert_eq!(parse_proc_cpu("cpu 1 2 3"), None);
        assert_eq!(parse_proc_cpu("cpu 10 20 30 40 50 60 70"), Some((190, 280)));
    }

    #[test]
    fn cgroup_memory_is_preferred_over_host_memory() {
        let (ratio, working_set) = read_memory(
            Some("50"),
            Some("100"),
            Some("MemTotal: 1000 kB\nMemAvailable: 1 kB"),
        );
        assert_eq!(ratio, Some(0.5));
        assert_eq!(working_set, 50);
    }

    #[test]
    fn stale_or_invalid_sample_is_not_fresh() {
        let sample = NodeRuntimeSample {
            sample_valid: true,
            sample_observed_at_unix_ms: 100,
            ..NodeRuntimeSample::default()
        };
        assert!(!sample.is_fresh(100 + NODE_RUNTIME_SAMPLE_MAX_AGE_MS + 1));
        assert!(!sample.is_fresh(99));
        assert!(!NodeRuntimeSample::default().is_fresh(100));
    }
}
