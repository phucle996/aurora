use super::super::MailRuntime;
use crate::config::Config;
use opentelemetry::metrics::Gauge;
use opentelemetry::{global, KeyValue};
use std::collections::HashMap;
use std::sync::{Arc, OnceLock};
use std::time::Duration;
use tokio_util::sync::CancellationToken;

static RUNTIME_HEALTH: OnceLock<Gauge<u64>> = OnceLock::new();
static RUNTIME_METRIC: OnceLock<Gauge<u64>> = OnceLock::new();

fn runtime_health() -> &'static Gauge<u64> {
    RUNTIME_HEALTH.get_or_init(|| {
        global::meter("aurora-dataplane")
            .u64_gauge("aurora_runtime_health")
            .with_description("Zone-local Mail consumer runtime state")
            .init()
    })
}

fn runtime_metric() -> &'static Gauge<u64> {
    RUNTIME_METRIC.get_or_init(|| {
        global::meter("aurora-dataplane")
            .u64_gauge("aurora_runtime_metric")
            .with_description("Zone-local Mail consumer runtime metrics")
            .init()
    })
}

/// Exports every fresh local Mail slot directly to the Zone OTel collector.
/// The collector owns Victoria delivery; no watch lease, Central Redis, JO
/// bridge, or reverse runtime stream participates in this read model.
pub(super) fn start_mail_consumer_runtime_telemetry(
    config: Arc<Config>,
    runtime: Arc<MailRuntime>,
    shutdown: CancellationToken,
) {
    tokio::spawn(async move {
        let mut previous = HashMap::<String, Vec<KeyValue>>::new();
        loop {
            if shutdown.is_cancelled() {
                return;
            }

            let mut current = HashMap::new();
            for snapshot in runtime.runtime_snapshots() {
                let state = match snapshot.state.as_str() {
                    "STOPPED" => 1,
                    "STARTING" => 2,
                    "RUNNING" => 3,
                    "PAUSED" => 4,
                    "DRAINING" => 5,
                    "ERROR" => 6,
                    "DEGRADED" => 7,
                    _ => continue,
                };
                let component_id = format!("slot-{}", snapshot.slot);
                let series_key = format!("{}:{component_id}", snapshot.consumer_id);
                let labels = vec![
                    KeyValue::new("aurora_module", "mail"),
                    KeyValue::new("aurora_resource_type", "consumer"),
                    KeyValue::new("aurora_resource_id", snapshot.consumer_id),
                    KeyValue::new("aurora_owner_id", snapshot.owner_id),
                    KeyValue::new("aurora_owner_type", snapshot.owner_type),
                    KeyValue::new("aurora_workspace_id", snapshot.workspace_id),
                    KeyValue::new("aurora_zone_id", snapshot.zone_id),
                    KeyValue::new("aurora_component_id", component_id),
                ];
                runtime_health().record(state, &labels);

                let mut lag_labels = labels.clone();
                lag_labels.push(KeyValue::new("aurora_metric", "consumer_lag"));
                runtime_metric().record(snapshot.consumer_lag, &lag_labels);
                current.insert(series_key, labels);
            }

            for (series_key, labels) in previous.drain() {
                if current.contains_key(&series_key) {
                    continue;
                }
                runtime_health().record(0, &labels);
                let mut lag_labels = labels;
                lag_labels.push(KeyValue::new("aurora_metric", "consumer_lag"));
                runtime_metric().record(0, &lag_labels);
            }
            previous = current;

            tokio::select! {
                _ = shutdown.cancelled() => return,
                _ = tokio::time::sleep(Duration::from_millis(
                    config.mail_runtime_telemetry_interval_ms
                        + rand::random::<u64>() % 1_000,
                )) => {}
            }
        }
    });
}
