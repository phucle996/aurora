pub mod core;
pub mod vps;

use crate::executor::{ExecutionResult, Executor, ExecutorError};
use crate::job_runtime::model::ValidatedJob;
use crate::observability::logger::Logger;
use std::sync::Arc;

/// ============================================================================
/// 📂 MODULE: executor/hypervisor/mod.rs - BỘ ĐỊNH TUYẾN NỘI BỘ CHO WORKLOAD VPS
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Đóng vai trò là Sub-Router phân phối các Action cụ thể trong phân hệ VPS.
///   - Giải quyết bài toán: `workload.action` (ví dụ: `vps.create` và `vps.resize`).
///
pub async fn dispatch_vps_job(
    action: &str,
    payload: Arc<ValidatedJob>,
) -> Result<ExecutionResult, ExecutorError> {
    Logger::sys_info(
        "executor.vps.router",
        &format!(
            "VPS Sub-Router: Dispatching action '{}' for job_id={}",
            action, payload.job_id
        ),
    );

    match action {
        "create" | "resize" => {
            let exec = vps::VpsExecutor;
            exec.execute(payload).await
        }
        _ => Err(ExecutorError::ExecutionFailed(format!(
            "Unsupported VPS action: {}",
            action
        ))),
    }
}
