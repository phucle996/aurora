use std::sync::atomic::AtomicUsize;
use std::sync::Arc;

use crate::config::Config;
use crate::infra::zone_kv::ZoneKvStore;
use crate::job_lifecycle::lease::JobExecutionLeaseRetry;
use crate::job_lifecycle::message::JobPayload;
use crate::job_lifecycle::runner::JobRunnerContext;
use crate::workerpool::lease_watchdog::JobExecutionLeaseRegistry;

/// Immutable wiring shared by every dynamically created worker slot.
///
/// Scale decisions only pass this handle; they cannot accidentally construct a
/// worker with a different Kafka transport, Zone KV, registry or admission
/// counter than the rest of the pod.
pub struct WorkerJobRuntime {
    config: Arc<Config>,
    job_runner_context: Arc<JobRunnerContext>,
    job_receiver: async_channel::Receiver<JobPayload>,
}

impl WorkerJobRuntime {
    pub fn new(
        config: Arc<Config>,
        kafka: Arc<crate::infra::kafka::KafkaTransport>,
        zone_kv: Arc<ZoneKvStore>,
        job_execution_lease_registry: Arc<JobExecutionLeaseRegistry>,
        job_receiver: async_channel::Receiver<JobPayload>,
        admitted_jobs: Arc<AtomicUsize>,
        job_execution_lease_retry_tx: tokio::sync::mpsc::Sender<JobExecutionLeaseRetry>,
    ) -> Self {
        let job_runner_context = Arc::new(JobRunnerContext::new(
            kafka,
            zone_kv,
            job_execution_lease_registry,
            admitted_jobs,
            job_execution_lease_retry_tx,
            config.zone_id.clone(),
        ));
        Self {
            config,
            job_runner_context,
            job_receiver,
        }
    }

    pub async fn receive_job(&self) -> Option<JobPayload> {
        self.job_receiver.recv().await.ok()
    }

    pub fn config(&self) -> &Arc<Config> {
        &self.config
    }

    pub fn zone_kv(&self) -> &Arc<ZoneKvStore> {
        self.job_runner_context.zone_kv()
    }

    pub fn job_runner_context(&self) -> &Arc<JobRunnerContext> {
        &self.job_runner_context
    }
}
