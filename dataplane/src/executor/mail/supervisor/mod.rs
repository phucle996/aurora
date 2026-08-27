mod consumer_telemetry;
mod local_observer;
mod metrics;

use super::MailRuntime;
use crate::config::Config;
use crate::infra::zone_kv::ZoneKvStore;
use serde::{Deserialize, Serialize};
use std::sync::Arc;
use tokio_util::sync::CancellationToken;

pub use metrics::MailWorkloadMetrics;

#[derive(Clone, Deserialize, Serialize)]
pub(crate) struct LocalMailNodeSnapshot {
    pub(crate) node_id: String,
    pub(crate) boot_id: String,
    pub(crate) pending_items: usize,
    pub(crate) in_flight_batches: usize,
    pub(crate) queue_capacity: usize,
    pub(crate) observed_at_unix_ms: u64,
}

/// Mỗi pod chỉ xuất local capacity snapshot và consumer telemetry. JMAP/Stalwart
/// health probing cùng Zone aggregation thuộc assigned Zone Control work.
pub struct MailWorkloadSupervisor;

impl MailWorkloadSupervisor {
    pub fn start_mail_runtime_observation(
        config: Arc<Config>,
        zone_kv: Arc<ZoneKvStore>,
        runtime: Arc<MailRuntime>,
        shutdown: CancellationToken,
    ) {
        local_observer::start_mail_dataplane_local_snapshot_writer(
            config.clone(),
            zone_kv,
            runtime.clone(),
            shutdown.clone(),
        );
        consumer_telemetry::start_mail_consumer_runtime_telemetry(config, runtime, shutdown);
    }
}
