mod admission;
mod apply;
mod delete;
mod entity;
mod kubernetes;
mod renderer;
mod result;

#[cfg(test)]
mod test;

pub use kubernetes::KubernetesRuntime;

use std::sync::Arc;

use crate::executor::{ExecutionResult, ExecutorError};
use crate::job_runtime::model::ValidatedJob;

pub async fn dispatch_managed_service_job(
    action: &str,
    job: Arc<ValidatedJob>,
    runtime: Arc<KubernetesRuntime>,
) -> Result<ExecutionResult, ExecutorError> {
    if action != "instance.execute" {
        return Err(ExecutorError::ExecutionFailed(
            "managed service action is not registered".to_string(),
        ));
    }
    runtime.execute(job).await
}
