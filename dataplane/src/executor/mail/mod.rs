pub mod executor;
pub mod processor;
pub mod projection;
pub mod runtime;
pub mod supervisor;

// [COMMENT]: Contract desired-state được compile một lần và dùng trực tiếp bởi bốn projection flow tách biệt.
pub mod runtime_proto {
    include!(concat!(env!("OUT_DIR"), "/mail.runtime.v1.rs"));
}

pub use executor::dispatch_mail_job;

use crate::config::Config;
use crate::infra::zone_kv::ZoneKvStore;
use processor::{JmapClient, MailBatcherHandle, MailMessageProcessor, SenderProfile};
use std::sync::Arc;
use supervisor::MailWorkloadMetrics;

/// [COMMENT]: MailRuntime là dependency dùng chung toàn pod; executor từng job không được tạo HTTP client/batcher riêng.
pub struct MailRuntime {
    pub batcher: Arc<MailBatcherHandle>,
    /// [COMMENT]: Phase-5 desired configuration là pod-local disposable state; Phase 6 chỉ đọc immutable snapshots từ đây.
    pub configuration: Arc<runtime::MailConfigurationRuntime>,
    /// [COMMENT]: Phase-6 supervisor sở hữu slot leases và broker connections; executor job không tự mở consumer.
    pub consumer_supervisor: Arc<runtime::MailConsumerSupervisor>,
    pub sender: Arc<SenderProfile>,
    pub metrics: Arc<MailWorkloadMetrics>,
    jmap: Arc<JmapClient>,
}

impl MailRuntime {
    pub fn new(config: &Config, zone_kv: Arc<ZoneKvStore>) -> Result<Arc<Self>, String> {
        let sender = Arc::new(SenderProfile::from_config(config)?);
        let jmap = Arc::new(JmapClient::new(config, sender.clone())?);
        let metrics = Arc::new(MailWorkloadMetrics::default());
        let batcher = MailBatcherHandle::start(config, jmap.clone(), metrics.clone());
        let configuration = runtime::MailConfigurationRuntime::new(config, zone_kv.clone());
        let processor = MailMessageProcessor::new(
            config,
            configuration.clone(),
            zone_kv.clone(),
            batcher.clone(),
            sender.clone(),
        );
        let consumer_supervisor =
            runtime::MailConsumerSupervisor::new(config, configuration.clone(), zone_kv, processor);
        Ok(Arc::new(Self {
            batcher,
            configuration,
            consumer_supervisor,
            sender,
            metrics,
            jmap,
        }))
    }

    pub async fn healthcheck(&self) -> Result<(), String> {
        self.jmap.healthcheck().await
    }

    pub async fn shutdown(&self) {
        // [COMMENT]: Fence broker intake trước, sau đó mới dừng config listener và transport JMAP.
        self.consumer_supervisor.shutdown().await;
        self.configuration.shutdown().await;
        self.batcher.shutdown().await;
    }
}
