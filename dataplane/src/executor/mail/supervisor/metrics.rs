use opentelemetry::metrics::{Counter, Gauge, Histogram};
use opentelemetry::{global, KeyValue};
use std::sync::atomic::{AtomicU64, AtomicUsize};
use std::sync::OnceLock;

static SERVICE_STATE: OnceLock<Gauge<u64>> = OnceLock::new();
static OPERATIONAL_OBSERVED_AT: OnceLock<Gauge<u64>> = OnceLock::new();
static CAPACITY_PERCENT: OnceLock<Gauge<u64>> = OnceLock::new();
static PENDING_ITEMS: OnceLock<Gauge<u64>> = OnceLock::new();
static IN_FLIGHT_BATCHES: OnceLock<Gauge<u64>> = OnceLock::new();
static ACTIVE_CONSUMER_SLOTS: OnceLock<Gauge<u64>> = OnceLock::new();
static DATAPLANE_NODES: OnceLock<Gauge<u64>> = OnceLock::new();
static STALWART_NODES: OnceLock<Gauge<u64>> = OnceLock::new();
static JMAP_PROBE_SUCCESS: OnceLock<Gauge<u64>> = OnceLock::new();
static JMAP_PROBE_DURATION: OnceLock<Histogram<f64>> = OnceLock::new();
static JMAP_LAST_SUCCESS: OnceLock<Gauge<u64>> = OnceLock::new();
static OBSERVATION_ERRORS: OnceLock<Counter<u64>> = OnceLock::new();

fn service_state_metric() -> &'static Gauge<u64> {
    SERVICE_STATE.get_or_init(|| {
        global::meter("aurora-dataplane")
            .u64_gauge("mail_service_health_state")
            .with_description("Mail service health: 0=down, 1=degraded, 2=healthy")
            .init()
    })
}

fn operational_observed_at_metric() -> &'static Gauge<u64> {
    OPERATIONAL_OBSERVED_AT.get_or_init(|| {
        global::meter("aurora-dataplane")
            .u64_gauge("mail_operational_observed_unix_seconds")
            .with_description("Unix timestamp fencing the most recent Mail operational snapshot")
            .init()
    })
}

fn capacity_metric() -> &'static Gauge<u64> {
    CAPACITY_PERCENT.get_or_init(|| {
        global::meter("aurora-dataplane")
            .u64_gauge("mail_service_capacity_percent")
            .with_description("Available Mail delivery capacity in percent")
            .init()
    })
}

fn pending_metric() -> &'static Gauge<u64> {
    PENDING_ITEMS.get_or_init(|| {
        global::meter("aurora-dataplane")
            .u64_gauge("mail_pending_items")
            .with_description("Mail items waiting in the zonal batch queues")
            .init()
    })
}

fn in_flight_metric() -> &'static Gauge<u64> {
    IN_FLIGHT_BATCHES.get_or_init(|| {
        global::meter("aurora-dataplane")
            .u64_gauge("mail_in_flight_batches")
            .with_description("Mail JMAP batches currently in flight")
            .init()
    })
}

fn active_consumer_slots_metric() -> &'static Gauge<u64> {
    ACTIVE_CONSUMER_SLOTS.get_or_init(|| {
        global::meter("aurora-dataplane")
            .u64_gauge("mail_active_consumer_slots")
            .with_description("Pod-local fresh logical Mail consumer slots")
            .init()
    })
}

fn dataplane_nodes_metric() -> &'static Gauge<u64> {
    DATAPLANE_NODES.get_or_init(|| {
        global::meter("aurora-dataplane")
            .u64_gauge("mail_dataplane_nodes")
            .with_description("Fresh Mail Dataplane nodes split by bounded health state")
            .init()
    })
}

fn stalwart_nodes_metric() -> &'static Gauge<u64> {
    STALWART_NODES.get_or_init(|| {
        global::meter("aurora-dataplane")
            .u64_gauge("mail_stalwart_nodes")
            .with_description("Stalwart cluster nodes split by bounded registry state")
            .init()
    })
}

fn jmap_probe_success_metric() -> &'static Gauge<u64> {
    JMAP_PROBE_SUCCESS.get_or_init(|| {
        global::meter("aurora-dataplane")
            .u64_gauge("mail_jmap_probe_success")
            .with_description("Most recent rotating JMAP health probe result")
            .init()
    })
}

fn jmap_probe_duration_metric() -> &'static Histogram<f64> {
    JMAP_PROBE_DURATION.get_or_init(|| {
        global::meter("aurora-dataplane")
            .f64_histogram("mail_jmap_probe_duration_seconds")
            .with_description("Duration of the rotating JMAP health probe")
            .init()
    })
}

fn jmap_last_success_metric() -> &'static Gauge<u64> {
    JMAP_LAST_SUCCESS.get_or_init(|| {
        global::meter("aurora-dataplane")
            .u64_gauge("mail_jmap_probe_last_success_unix_seconds")
            .with_description("Unix timestamp of the last successful JMAP health probe")
            .init()
    })
}

fn observation_error_metric() -> &'static Counter<u64> {
    OBSERVATION_ERRORS.get_or_init(|| {
        global::meter("aurora-dataplane")
            .u64_counter("mail_operational_observation_error_total")
            .with_description("Bounded Mail health observation errors")
            .init()
    })
}

/// [COMMENT]: Snapshot này chỉ mang low-cardinality operational aggregates sang OTel/Grafana.
pub(super) struct MailOperationalMetricsSnapshot {
    pub observed_at_unix_seconds: u64,
    pub state: u64,
    pub capacity_percent: u64,
    pub pending_items: u64,
    pub in_flight_batches: u64,
    pub dataplane_nodes_healthy: u64,
    pub dataplane_nodes_degraded: u64,
    pub dataplane_nodes_down: u64,
    pub stalwart_nodes_active: u64,
    pub stalwart_nodes_stale: u64,
    pub stalwart_nodes_inactive: u64,
}

/// [COMMENT]: Một bộ đếm dùng chung toàn pod; processor chỉ cập nhật, supervisor chỉ đọc để tính capacity.
#[derive(Default)]
pub struct MailWorkloadMetrics {
    pub pending_items: AtomicUsize,
    pub in_flight_batches: AtomicUsize,
    pub accepted_total: AtomicU64,
    pub failed_total: AtomicU64,
}

impl MailWorkloadMetrics {
    pub(super) fn record_jmap_probe(
        &self,
        success: bool,
        duration_seconds: f64,
        last_success_unix_seconds: u64,
    ) {
        jmap_probe_success_metric().record(u64::from(success), &[]);
        jmap_probe_duration_metric().record(duration_seconds, &[]);
        jmap_last_success_metric().record(last_success_unix_seconds, &[]);
    }

    pub(super) fn record_operational_snapshot(&self, snapshot: &MailOperationalMetricsSnapshot) {
        // [COMMENT]: Dashboard dùng timestamp này để bỏ chuỗi metric cũ khi rotating lease đổi holder.
        operational_observed_at_metric().record(snapshot.observed_at_unix_seconds, &[]);
        service_state_metric().record(snapshot.state, &[]);
        capacity_metric().record(snapshot.capacity_percent, &[]);
        pending_metric().record(snapshot.pending_items, &[]);
        in_flight_metric().record(snapshot.in_flight_batches, &[]);
        for (state, count) in [
            ("healthy", snapshot.dataplane_nodes_healthy),
            ("degraded", snapshot.dataplane_nodes_degraded),
            ("down", snapshot.dataplane_nodes_down),
        ] {
            dataplane_nodes_metric().record(count, &[KeyValue::new("state", state)]);
        }
        for (state, count) in [
            ("active", snapshot.stalwart_nodes_active),
            ("stale", snapshot.stalwart_nodes_stale),
            ("inactive", snapshot.stalwart_nodes_inactive),
        ] {
            stalwart_nodes_metric().record(count, &[KeyValue::new("state", state)]);
        }
    }

    pub(super) fn record_local_runtime_slots(&self, active_consumer_slots: u64) {
        // [COMMENT]: OTel resource attributes identify the pod; no consumer/customer label and no
        // intermediate NATS KV snapshot are involved.
        active_consumer_slots_metric().record(active_consumer_slots, &[]);
    }

    pub(super) fn record_observation_error(&self, error_code: &str) {
        if !error_code.is_empty() {
            observation_error_metric()
                .add(1, &[KeyValue::new("error_code", error_code.to_string())]);
        }
    }
}
