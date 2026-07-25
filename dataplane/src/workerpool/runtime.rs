use std::sync::Arc;

use crate::config::Config;
use crate::infra::zone_kv::ZoneKvStore;
use crate::job_runtime::execution::JobExecutionRuntime;
use crate::job_runtime::model::QueuedJob;

/// Immutable wiring shared by every dynamically created worker slot.
///
/// Scale decisions only pass this handle; they cannot accidentally construct a
/// worker with a different Kafka transport, Zone KV, registry or admission
/// counter than the rest of the pod.
pub struct WorkerJobRuntime {
    config: Arc<Config>,
    execution_runtime: Arc<JobExecutionRuntime>,
    job_receiver: async_channel::Receiver<QueuedJob>,
}

impl WorkerJobRuntime {
    pub fn new(
        config: Arc<Config>,
        execution_runtime: Arc<JobExecutionRuntime>,
        job_receiver: async_channel::Receiver<QueuedJob>,
    ) -> Self {
        Self {
            config,
            execution_runtime,
            job_receiver,
        }
    }

    pub async fn receive_job(&self) -> Option<QueuedJob> {
        self.job_receiver.recv().await.ok()
    }

    pub fn config(&self) -> &Arc<Config> {
        &self.config
    }

    pub fn zone_kv(&self) -> &Arc<ZoneKvStore> {
        self.execution_runtime.zone_kv()
    }

    pub fn execution_runtime(&self) -> &Arc<JobExecutionRuntime> {
        &self.execution_runtime
    }
}
