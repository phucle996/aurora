use super::processor;
use super::HypervisorRuntime;
use crate::executor::{ExecutionResult, ExecutorError};
use crate::job_runtime::model::ValidatedJob;
use std::sync::Arc;

pub async fn dispatch_hypervisor_job(
    action: &str,
    payload: Arc<ValidatedJob>,
    runtime: Arc<HypervisorRuntime>,
) -> Result<ExecutionResult, ExecutorError> {
    if action == "vm.create" {
        return processor::execute_vm_create(payload, runtime).await;
    }

    Err(ExecutorError::ExecutionFailed(format!(
        "Unsupported hypervisor action: {action}"
    )))
}
