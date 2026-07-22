mod backpressure;
mod metrics;
mod runtime_reporter;
mod workload_monitor;

use super::MailRuntime;
use crate::config::Config;
use crate::infra::redis::RedisClientManager;
use crate::infra::zone_kv::ZoneKvStore;
use std::sync::Arc;

pub use metrics::MailWorkloadMetrics;

/// [COMMENT]: Mail tự sở hữu health và reverse runtime report; Zone Gateway chỉ còn tổng hợp snapshot cấp Zone.
pub struct MailWorkloadSupervisor;

impl MailWorkloadSupervisor {
    pub fn start(
        config: Arc<Config>,
        zone_kv: Arc<ZoneKvStore>,
        redis_job: Arc<RedisClientManager>,
        runtime: Arc<MailRuntime>,
    ) {
        workload_monitor::start(config.clone(), zone_kv.clone(), runtime);
        runtime_reporter::start(config, zone_kv, redis_job);
    }
}
