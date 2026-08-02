use std::sync::atomic::{AtomicU64, AtomicUsize, Ordering};

#[derive(Default)]
pub struct Telemetry {
    connections_active: AtomicUsize,
    connections_rejected_total: AtomicU64,
    fanout_groups_active: AtomicUsize,
    source_queries_total: AtomicU64,
    source_errors_total: AtomicU64,
    gap_events_total: AtomicU64,
    stream_expired_total: AtomicU64,
}

impl Telemetry {
    pub fn connection_opened(&self) {
        self.connections_active.fetch_add(1, Ordering::Relaxed);
    }

    pub fn connection_closed(&self) {
        let _ =
            self.connections_active
                .fetch_update(Ordering::Relaxed, Ordering::Relaxed, |value| {
                    value.checked_sub(1)
                });
    }

    pub fn connection_rejected(&self) {
        self.connections_rejected_total
            .fetch_add(1, Ordering::Relaxed);
    }

    pub fn fanout_group_opened(&self) {
        self.fanout_groups_active.fetch_add(1, Ordering::Relaxed);
    }

    pub fn fanout_group_closed(&self) {
        let _ =
            self.fanout_groups_active
                .fetch_update(Ordering::Relaxed, Ordering::Relaxed, |value| {
                    value.checked_sub(1)
                });
    }

    pub fn source_query(&self) {
        self.source_queries_total.fetch_add(1, Ordering::Relaxed);
    }

    pub fn source_error(&self) {
        self.source_errors_total.fetch_add(1, Ordering::Relaxed);
    }

    pub fn gap_event(&self) {
        self.gap_events_total.fetch_add(1, Ordering::Relaxed);
    }

    pub fn stream_expired(&self) {
        self.stream_expired_total.fetch_add(1, Ordering::Relaxed);
    }

    pub fn prometheus(&self) -> String {
        format!(
            concat!(
                "# TYPE aurora_zone_runtime_stream_connections_active gauge\n",
                "aurora_zone_runtime_stream_connections_active {}\n",
                "# TYPE aurora_zone_runtime_stream_connections_rejected_total counter\n",
                "aurora_zone_runtime_stream_connections_rejected_total {}\n",
                "# TYPE aurora_zone_runtime_stream_fanout_groups_active gauge\n",
                "aurora_zone_runtime_stream_fanout_groups_active {}\n",
                "# TYPE aurora_zone_runtime_stream_source_queries_total counter\n",
                "aurora_zone_runtime_stream_source_queries_total {}\n",
                "# TYPE aurora_zone_runtime_stream_source_errors_total counter\n",
                "aurora_zone_runtime_stream_source_errors_total {}\n",
                "# TYPE aurora_zone_runtime_stream_gap_events_total counter\n",
                "aurora_zone_runtime_stream_gap_events_total {}\n",
                "# TYPE aurora_zone_runtime_stream_expired_total counter\n",
                "aurora_zone_runtime_stream_expired_total {}\n"
            ),
            self.connections_active.load(Ordering::Relaxed),
            self.connections_rejected_total.load(Ordering::Relaxed),
            self.fanout_groups_active.load(Ordering::Relaxed),
            self.source_queries_total.load(Ordering::Relaxed),
            self.source_errors_total.load(Ordering::Relaxed),
            self.gap_events_total.load(Ordering::Relaxed),
            self.stream_expired_total.load(Ordering::Relaxed),
        )
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn prometheus_contract_has_no_runtime_scope_labels() {
        let telemetry = Telemetry::default();
        telemetry.connection_opened();
        telemetry.source_query();
        let output = telemetry.prometheus();
        assert!(output.contains("connections_active 1"));
        assert!(!output.contains("resource_id"));
        assert!(!output.contains("owner_id"));
    }

    #[test]
    fn close_counters_never_underflow() {
        let telemetry = Telemetry::default();
        telemetry.connection_closed();
        telemetry.fanout_group_closed();
        let output = telemetry.prometheus();
        assert!(output.contains("connections_active 0"));
        assert!(output.contains("fanout_groups_active 0"));
    }
}
