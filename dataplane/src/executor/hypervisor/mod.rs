pub mod vps;

use crate::executor::{Executor, ExecutorError, ExecutionResult};
use crate::job_receiver::message::JobPayload;

/// ============================================================================
/// 📂 MODULE: executor/hypervisor/mod.rs - BỘ ĐỊNH TUYẾN NỘI BỘ CHO WORKLOAD VPS
/// ============================================================================
/// 
/// 📌 VAI TRÒ (ROLE):
///   - Đóng vai trò là Sub-Router phân phối các Action cụ thể trong phân hệ VPS.
///   - Giải quyết bài toán: `workload.action` (ví dụ: `vps.create` và `vps.resize`).
///

pub async fn dispatch_vps_job(action: &str, payload: JobPayload) -> Result<ExecutionResult, ExecutorError> {
    crate::observability::logger::Logger::sys_info(
        "executor.vps.router",
        &format!("VPS Sub-Router: Dispatching action '{}' for job_id={}", action, payload.job_id),
    );

    match action {
        "create" | "resize" => {
            let exec = vps::VpsExecutor;
            exec.execute(payload).await
        }
        _ => Err(ExecutorError::ExecutionFailed(format!("Unsupported VPS action: {}", action))),
    }
}
