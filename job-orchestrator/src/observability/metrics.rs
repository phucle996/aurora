use std::sync::OnceLock;
use std::time::Duration;

use opentelemetry::metrics::{Counter, Histogram, Unit};
use opentelemetry::{global, KeyValue};

use crate::observability::logger::{LogFields, Logger};

const METER_NAME: &str = "aurora-job-orchestrator";
const PIPELINE_SAMPLE_INTERVAL: Duration = Duration::from_secs(10);

static WAL_RECORDS_ACCEPTED: OnceLock<Counter<u64>> = OnceLock::new();
static KAFKA_COMMANDS_PUBLISHED: OnceLock<Counter<u64>> = OnceLock::new();
static RESULTS_RECEIVED: OnceLock<Counter<u64>> = OnceLock::new();
static NOTIFICATIONS_ENQUEUED: OnceLock<Counter<u64>> = OnceLock::new();
static RECORD_OUTCOMES: OnceLock<Counter<u64>> = OnceLock::new();
static KAFKA_OPERATIONS: OnceLock<Counter<u64>> = OnceLock::new();
static KAFKA_OPERATION_DURATION: OnceLock<Histogram<f64>> = OnceLock::new();
static WORKER_TERMINATIONS: OnceLock<Counter<u64>> = OnceLock::new();

pub struct MetricsManager;

impl MetricsManager {
    pub fn init() {
        let _ = Self::wal_records_accepted();
        let _ = Self::kafka_commands_published();
        let _ = Self::results_received();
        let _ = Self::notifications_enqueued();
        let _ = Self::record_outcomes();
        let _ = Self::kafka_operations();
        let _ = Self::kafka_operation_duration();
        let _ = Self::worker_terminations();

        Logger::sys_info_with_fields(
            "metrics.init",
            "METRICS_INITIALIZED",
            "OpenTelemetry metric instruments initialized",
            LogFields {
                outcome: Some("ready"),
                ..LogFields::default()
            },
        );
    }

    fn wal_records_accepted() -> &'static Counter<u64> {
        WAL_RECORDS_ACCEPTED.get_or_init(|| {
            global::meter(METER_NAME)
                .u64_counter("job_orchestrator_wal_records_accepted_total")
                .with_description(
                    "Validated PostgreSQL outbox WAL records accepted for durable publication",
                )
                .init()
        })
    }

    fn kafka_commands_published() -> &'static Counter<u64> {
        KAFKA_COMMANDS_PUBLISHED.get_or_init(|| {
            global::meter(METER_NAME)
                .u64_counter("job_orchestrator_kafka_commands_published_total")
                .with_description("Durable Zone commands acknowledged by Kafka")
                .init()
        })
    }

    fn results_received() -> &'static Counter<u64> {
        RESULTS_RECEIVED.get_or_init(|| {
            global::meter(METER_NAME)
                .u64_counter("job_orchestrator_results_received_total")
                .with_description("Strictly validated Dataplane result records received from Kafka")
                .init()
        })
    }

    fn notifications_enqueued() -> &'static Counter<u64> {
        NOTIFICATIONS_ENQUEUED.get_or_init(|| {
            global::meter(METER_NAME)
                .u64_counter("job_orchestrator_notifications_enqueued_total")
                .with_description(
                    "Job notifications durably enqueued into the bounded Shared Redis stream",
                )
                .init()
        })
    }

    fn record_outcomes() -> &'static Counter<u64> {
        RECORD_OUTCOMES.get_or_init(|| {
            global::meter(METER_NAME)
                .u64_counter("job_orchestrator_record_outcomes_total")
                .with_description(
                    "Bounded outcome taxonomy for WAL, result, notification, and DLQ records",
                )
                .init()
        })
    }

    fn kafka_operations() -> &'static Counter<u64> {
        KAFKA_OPERATIONS.get_or_init(|| {
            global::meter(METER_NAME)
                .u64_counter("job_orchestrator_kafka_operations_total")
                .with_description("Kafka transport operations by operation and terminal outcome")
                .init()
        })
    }

    fn kafka_operation_duration() -> &'static Histogram<f64> {
        KAFKA_OPERATION_DURATION.get_or_init(|| {
            global::meter(METER_NAME)
                .f64_histogram("job_orchestrator_kafka_operation_duration_seconds")
                .with_unit(Unit::new("s"))
                .with_description("End-to-end latency of logical Kafka transport operations")
                .init()
        })
    }

    fn worker_terminations() -> &'static Counter<u64> {
        WORKER_TERMINATIONS.get_or_init(|| {
            global::meter(METER_NAME)
                .u64_counter("job_orchestrator_worker_terminations_total")
                .with_description("Unexpected terminal exits of critical runtime workers")
                .init()
        })
    }

    pub fn inc_wal_records_accepted() {
        Self::wal_records_accepted().add(1, &[]);
        Self::record_outcome("wal_outbox", "accepted");
    }

    pub fn record_wal_rejected() {
        Self::record_outcome("wal_outbox", "rejected");
    }

    pub fn record_managed_service_outbox_stale() {
        Self::record_outcome("managed_service_outbox", "stale");
    }

    pub fn inc_kafka_commands_published() {
        Self::kafka_commands_published().add(1, &[]);
        Self::record_outcome("zone_command", "published");
    }

    pub fn inc_results_received() {
        Self::results_received().add(1, &[]);
        Self::record_outcome("job_result", "received");
    }

    pub fn record_result_settled() {
        Self::record_outcome("job_result", "settled");
    }

    pub fn record_result_failed() {
        Self::record_outcome("job_result", "failed");
    }

    pub fn record_result_rejected() {
        Self::record_outcome("job_result", "rejected");
    }

    pub fn record_dlq_published() {
        Self::record_outcome("dead_letter", "published");
    }

    pub fn inc_notifications_enqueued() {
        Self::notifications_enqueued().add(1, &[]);
        Self::record_outcome("notification", "enqueued");
    }

    pub fn record_notification_failed() {
        Self::record_outcome("notification", "failed");
    }

    pub fn record_ownership_enqueued() {
        Self::record_outcome("resource_ownership", "enqueued");
    }

    pub fn record_ownership_pending() {
        Self::record_outcome("resource_ownership", "pending");
    }

    pub fn record_kafka_operation(
        operation: &'static str,
        outcome: &'static str,
        elapsed: Duration,
    ) {
        let attributes = [
            KeyValue::new("operation", operation),
            KeyValue::new("outcome", outcome),
        ];
        Self::kafka_operations().add(1, &attributes);
        Self::kafka_operation_duration().record(
            elapsed.as_secs_f64(),
            &[KeyValue::new("operation", operation)],
        );
    }

    pub fn record_worker_termination(worker: &'static str) {
        Self::worker_terminations().add(1, &[KeyValue::new("worker", worker)]);
    }

    fn record_outcome(record_type: &'static str, outcome: &'static str) {
        Self::record_outcomes().add(
            1,
            &[
                KeyValue::new("record_type", record_type),
                KeyValue::new("outcome", outcome),
            ],
        );
    }

    /// Keeps logger loss/suppression counters fresh without adding work to hot transport paths.
    pub async fn run_pipeline_sampler() {
        let mut interval = tokio::time::interval(PIPELINE_SAMPLE_INTERVAL);
        interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
        loop {
            interval.tick().await;
            Logger::record_pipeline_metrics();
        }
    }
}
