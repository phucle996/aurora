mod bucket_scanner;
mod hypervisor_probe;
mod mail_probe;
mod metadata_listener;
mod metadata_repair;
mod report_publisher;
mod scale_controller;
mod scale_policy;
mod session;
mod storage_probe;
mod supervisor;

use std::sync::Arc;

use tokio_util::sync::CancellationToken;

use crate::config::Config;
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
        shutdown: CancellationToken,
        task_guard: TaskGuard,
    ) {
        tokio::spawn(async move {
            let _task_guard = task_guard;
            supervisor::run_zone_leader_supervisor(config, zone_kv, kafka, mail_runtime, shutdown)
                .await;
        });
    }
}
