use crate::executor::{ExecutionResult, Executor, ExecutorError};
use crate::infra::zone_kv::ZoneKvStore;
use crate::job_runtime::model::ValidatedJob;
use crate::observability::logger::Logger;
use std::sync::Arc;

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
    payload: Arc<ValidatedJob>,
    zone_kv: Arc<ZoneKvStore>,
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
            let exec = super::bucket::BucketCreateExecutor::new(zone_kv.clone());
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

        // [COMMENT]: Định tuyến cấu hình Versioning bucket
        "bucket.versioning" => {
            let exec = super::BucketVersioningExecutor;
            exec.execute(payload).await
        }

        // [COMMENT]: Định tuyến cấu hình Lifecycle rules bucket
        "bucket.lifecycle" => {
            let exec = super::BucketLifecycleExecutor;
            exec.execute(payload).await
        }

        // [COMMENT]: Định tuyến xóa bucket
        "bucket.delete" => {
            let exec = super::BucketDeleteExecutor::new(zone_kv.clone());
            exec.execute(payload).await
        }

        "access.prepare" => {
            let exec = super::StorageAccessPrepareExecutor::new(zone_kv);
            exec.execute(payload).await
        }

        "bucket.commercial_admission" => {
            let exec = super::BucketCommercialAdmissionExecutor::new(zone_kv);
            exec.execute(payload).await
        }

        // Gặp hành động không được hỗ trợ
        _ => Err(ExecutorError::ExecutionFailed(format!(
            "Unsupported storage action: {}",
            action
        ))),
    }
}
