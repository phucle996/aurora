pub mod batcher;
pub mod executor;
pub mod jmap;
pub mod model;
pub mod monitor;
pub mod projection;
pub mod runtime_configuration;
pub mod stream_supervisor;
pub mod template;

// [COMMENT]: Contract desired-state được compile một lần và dùng trực tiếp bởi bốn projection flow tách biệt.
pub mod runtime_proto {
    include!(concat!(env!("OUT_DIR"), "/mail.runtime.v1.rs"));
}

pub use executor::dispatch_mail_job;

use crate::config::Config;
use crate::infra::zone_kv::ZoneKvStore;
use batcher::{MailBatcherHandle, MailBatcherStats};
use jmap::JmapClient;
use model::SenderProfile;
use std::sync::Arc;

/// [COMMENT]: MailRuntime là dependency dùng chung toàn pod; executor từng job không được tạo HTTP client/batcher riêng.
pub struct MailRuntime {
    pub batcher: Arc<MailBatcherHandle>,
    /// [COMMENT]: Phase-5 desired configuration là pod-local disposable state; Phase 6 chỉ đọc immutable snapshots từ đây.
    pub configuration: Arc<runtime_configuration::MailConfigurationRuntime>,
    /// [COMMENT]: Phase-6 supervisor sở hữu slot leases và broker connections; executor job không tự mở consumer.
    pub stream_supervisor: Arc<stream_supervisor::MailStreamSupervisor>,
    pub sender: Arc<SenderProfile>,
    pub stats: Arc<MailBatcherStats>,
    jmap: Arc<JmapClient>,
}

impl MailRuntime {
    pub fn new(config: &Config, zone_kv: Arc<ZoneKvStore>) -> Result<Arc<Self>, String> {
        let sender = Arc::new(SenderProfile::from_config(config)?);
        let jmap = Arc::new(JmapClient::new(config, sender.clone())?);
        let stats = Arc::new(MailBatcherStats::default());
        let batcher = MailBatcherHandle::start(config, jmap.clone(), stats.clone());
        let configuration =
            runtime_configuration::MailConfigurationRuntime::new(config, zone_kv.clone());
        let stream_supervisor =
            stream_supervisor::MailStreamSupervisor::new(config, configuration.clone(), zone_kv);
        Ok(Arc::new(Self {
            batcher,
            configuration,
            stream_supervisor,
            sender,
            stats,
            jmap,
        }))
    }

    pub async fn healthcheck(&self) -> Result<(), String> {
        self.jmap.healthcheck().await
    }

    pub async fn shutdown(&self) {
        // [COMMENT]: Fence broker intake trước, sau đó mới dừng config listener và transport JMAP.
        self.stream_supervisor.shutdown().await;
        self.configuration.shutdown().await;
        self.batcher.shutdown().await;
    }
}
