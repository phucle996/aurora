use opentelemetry::global;
use opentelemetry::metrics::Gauge;
use std::sync::atomic::{AtomicU64, AtomicUsize};
use std::sync::OnceLock;

static ACTIVE_CONSUMER_SLOTS: OnceLock<Gauge<u64>> = OnceLock::new();

fn active_consumer_slots_metric() -> &'static Gauge<u64> {
    ACTIVE_CONSUMER_SLOTS.get_or_init(|| {
        global::meter("aurora-dataplane")
            .u64_gauge("mail_active_consumer_slots")
            .with_description("Pod-local fresh logical Mail consumer slots")
            .init()
    })
}

/// Pod-local counters are consumed by the mail batcher and local runtime
/// snapshot writer. Zone-wide health metrics are produced by Zone Control.
#[derive(Default)]
pub struct MailWorkloadMetrics {
    pub pending_items: AtomicUsize,
    pub in_flight_batches: AtomicUsize,
    pub accepted_total: AtomicU64,
    pub failed_total: AtomicU64,
}

impl MailWorkloadMetrics {
    pub(super) fn record_local_runtime_slots(&self, active_consumer_slots: u64) {
        // OTel resource attributes identify the pod; no customer label and no
        // intermediate Zone-wide health projection are involved.
        active_consumer_slots_metric().record(active_consumer_slots, &[]);
    }
}
