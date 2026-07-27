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
    match action {
        "vm.create" => processor::execute_vm_create(payload, runtime).await,
        "image.import" => processor::execute_image_import(payload, runtime).await,
        "image.delete" => processor::execute_image_delete(payload, runtime).await,
        _ => Err(ExecutorError::ExecutionFailed(format!(
            "Unsupported hypervisor action: {action}"
        ))),
    }
}
