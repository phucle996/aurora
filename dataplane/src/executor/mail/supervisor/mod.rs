mod consumer_reporter;
mod local_observer;
mod metrics;

use super::MailRuntime;
use crate::config::Config;
use crate::infra::nats_core::NatsCoreTransport;
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

#[cfg(test)]
#[path = "../test/report_contract.rs"]
mod report_contract_tests;

/// [COMMENT]: Mọi pod chỉ xuất local runtime snapshot/report. JMAP/Stalwart
/// health probing and Zone aggregation belong to assigned Zone Control work.
pub struct MailWorkloadSupervisor;

impl MailWorkloadSupervisor {
    pub fn start_mail_runtime_reporting(
        config: Arc<Config>,
        zone_kv: Arc<ZoneKvStore>,
        nats_core: Arc<NatsCoreTransport>,
        runtime: Arc<MailRuntime>,
        shutdown: CancellationToken,
    ) {
        local_observer::start_mail_dataplane_local_snapshot_writer(
            config.clone(),
            zone_kv,
            runtime.clone(),
            shutdown.clone(),
        );
        consumer_reporter::start_mail_consumer_runtime_reporter(
            config, nats_core, runtime, shutdown,
        );
    }
}
