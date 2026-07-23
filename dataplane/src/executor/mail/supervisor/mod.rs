mod backpressure;
mod consumer_reporter;
mod health_observer;
mod metrics;

use super::MailRuntime;
use crate::config::Config;
use crate::infra::nats_core::NatsCoreTransport;
use crate::infra::zone_kv::ZoneKvStore;
use std::sync::Arc;

pub use metrics::MailWorkloadMetrics;

#[cfg(test)]
#[path = "../test/backpressure.rs"]
mod backpressure_tests;

#[cfg(test)]
#[path = "../test/report_contract.rs"]
mod report_contract_tests;

/// [COMMENT]: Customer runtime chỉ đi NATS Core khi có watch lease; Zone KV chỉ giữ
/// config/coordination và aggregate infra health cho OTel/Grafana. Dataplane không dùng Redis.
pub struct MailWorkloadSupervisor;

impl MailWorkloadSupervisor {
    pub fn start(
        config: Arc<Config>,
        zone_kv: Arc<ZoneKvStore>,
        nats_core: Arc<NatsCoreTransport>,
        runtime: Arc<MailRuntime>,
    ) {
        health_observer::start(config.clone(), zone_kv, runtime.clone());
        consumer_reporter::start(config, nats_core, runtime);
    }
}
