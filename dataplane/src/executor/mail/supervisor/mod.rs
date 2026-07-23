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

/// [COMMENT]: Customer runtime chỉ đi Central Redis khi có watch lease; Zone KV chỉ giữ
/// config/coordination và aggregate infra health cho OTel/Grafana.
pub struct MailWorkloadSupervisor;

impl MailWorkloadSupervisor {
    pub fn start(
        config: Arc<Config>,
        zone_kv: Arc<ZoneKvStore>,
        redis_job: Arc<RedisClientManager>,
        runtime: Arc<MailRuntime>,
    ) {
        health_observer::start(config.clone(), zone_kv, runtime.clone());
        consumer_reporter::start(config, redis_job, runtime);
    }
}
