mod infra;
mod leadership;
mod worker_scaling;
mod zone_metadata;
mod zone_report;

use std::sync::Arc;

use tokio_util::sync::CancellationToken;

use crate::config::Config;
use crate::executor::hypervisor::HypervisorRuntime;
use crate::executor::mail::MailRuntime;
use crate::infra::kafka::KafkaTransport;
use crate::infra::zone_kv::ZoneKvStore;
use crate::workerpool::pool::TaskGuard;

pub mod zone_report_proto {
    include!(concat!(env!("OUT_DIR"), "/zone.rs"));
}

/// [COMMENT]: Đây là entry point duy nhất cho mọi Zone-wide singleton responsibility.
/// Worker/executor module không tự bầu leader hoặc tự ping hạ tầng.
pub struct ZoneLeaderSupervisor;

impl ZoneLeaderSupervisor {
    pub fn start_zone_leader_supervisor(
        config: Arc<Config>,
        zone_kv: Arc<ZoneKvStore>,
        kafka: Arc<KafkaTransport>,
        mail_runtime: Arc<MailRuntime>,
        hypervisor_runtime: Arc<HypervisorRuntime>,
        shutdown: CancellationToken,
        task_guard: TaskGuard,
    ) {
        tokio::spawn(async move {
            let _task_guard = task_guard;
            leadership::run_zone_leader_supervisor(
                config,
                zone_kv,
                kafka,
                mail_runtime,
                hypervisor_runtime,
                shutdown,
            )
            .await;
        });
    }
}
