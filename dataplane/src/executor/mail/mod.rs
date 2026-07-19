pub mod batcher;
pub mod executor;
pub mod jmap;
pub mod model;
pub mod monitor;
pub mod template;

pub use executor::dispatch_mail_job;

use crate::config::Config;
use batcher::{MailBatcherHandle, MailBatcherStats};
use jmap::JmapClient;
use model::SenderProfile;
use std::sync::Arc;

/// [COMMENT]: MailRuntime là dependency dùng chung toàn pod; executor từng job không được tạo HTTP client/batcher riêng.
pub struct MailRuntime {
    pub batcher: Arc<MailBatcherHandle>,
    pub sender: Arc<SenderProfile>,
    pub stats: Arc<MailBatcherStats>,
    jmap: Arc<JmapClient>,
}

impl MailRuntime {
    pub fn new(config: &Config) -> Result<Arc<Self>, String> {
        let sender = Arc::new(SenderProfile::from_config(config)?);
        let jmap = Arc::new(JmapClient::new(config, sender.clone())?);
        let stats = Arc::new(MailBatcherStats::default());
        let batcher = MailBatcherHandle::start(config, jmap.clone(), stats.clone());
        Ok(Arc::new(Self {
            batcher,
            sender,
            stats,
            jmap,
        }))
    }

    pub async fn healthcheck(&self) -> Result<(), String> {
        self.jmap.healthcheck().await
    }

    pub async fn shutdown(&self) {
        self.batcher.shutdown().await;
    }
}
