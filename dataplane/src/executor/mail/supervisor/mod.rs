mod backpressure;
mod consumer_reporter;
mod health_observer;
mod metrics;

use super::MailRuntime;
use crate::config::Config;
use crate::infra::redis::RedisClientManager;
use crate::infra::zone_kv::ZoneKvStore;
use std::sync::Arc;

pub use metrics::MailWorkloadMetrics;

#[cfg(test)]
#[path = "../test/backpressure.rs"]
mod backpressure_tests;

#[cfg(test)]
#[path = "../test/report_contract.rs"]
mod report_contract_tests;

/// [COMMENT]: Consumer delta đi Central Redis; operational health chỉ ở Zone KV + OTel/Grafana.
pub struct MailWorkloadSupervisor;

impl MailWorkloadSupervisor {
    pub fn start(
        config: Arc<Config>,
        zone_kv: Arc<ZoneKvStore>,
        redis_job: Arc<RedisClientManager>,
        runtime: Arc<MailRuntime>,
    ) {
        health_observer::start(config.clone(), zone_kv.clone(), runtime);
        consumer_reporter::start(config, zone_kv, redis_job);
    }
}
