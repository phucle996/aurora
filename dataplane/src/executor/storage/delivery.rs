use crate::executor::{ExecutionResult, Executor, ExecutorError};
// Sử dụng JobPayload từ module job_lifecycle mới đổi tên
use crate::job_lifecycle::message::JobPayload;
use crate::observability::logger::Logger;

/// ============================================================================
/// 📂 MODULE: executor/storage/delivery.rs - BỘ ĐỊNH TUYẾN / PHÂN PHỐI WORKLOAD STORAGE
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Nhận yêu cầu định tuyến của các hành động (Action) cụ thể thuộc Storage Workload.
///   - Phân phối cuộc gọi tới các Bộ thực thi (Executor) độc lập tương ứng.
///
pub async fn dispatch_storage_job(
    action: &str,
    payload: JobPayload,
) -> Result<ExecutionResult, ExecutorError> {
    Logger::sys_info(
        "executor.storage.router",
        &format!(
            "Storage Workload Router: Routing action '{}' for job_id={}",
            action, payload.job_id
        ),
    );

    match action {
        // [COMMENT]: Định tuyến action bucket.create sang BucketCreateExecutor chuyên biệt
        "bucket.create" => {
            let exec = super::bucket::BucketCreateExecutor;
            exec.execute(payload).await
        }

        // [COMMENT]: Định tuyến tạo credential
        "credential.create" => {
            let exec = super::CredentialCreateExecutor;
            exec.execute(payload).await
        }

        // [COMMENT]: Định tuyến xóa credential
        "credential.delete" => {
            let exec = super::CredentialDeleteExecutor;
            exec.execute(payload).await
        }

        // [COMMENT]: Định tuyến thay đổi hạn mức quota bucket
        "bucket.resize" => {
            let exec = super::BucketResizeExecutor;
            exec.execute(payload).await
        }

        // [COMMENT]: Định tuyến xóa bucket
        "bucket.delete" => {
            let exec = super::BucketDeleteExecutor;
            exec.execute(payload).await
        }

        // Gặp hành động không được hỗ trợ
        _ => Err(ExecutorError::ExecutionFailed(format!(
            "Unsupported storage action: {}",
            action
        ))),
    }
}
