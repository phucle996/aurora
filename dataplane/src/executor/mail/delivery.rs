use crate::executor::{ExecutionResult, Executor, ExecutorError};
use crate::job_lifecycle::message::JobPayload;
use crate::observability::logger::Logger;
use std::sync::Arc;

/// ============================================================================
/// 📂 MODULE: executor/mail/delivery.rs - BỘ ĐỊNH TUYẾN / PHÂN PHỐI WORKLOAD MAIL
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Nhận yêu cầu định tuyến của các hành động (Action) cụ thể thuộc Mail Workload.
///   - Phân phối cuộc gọi tới các Bộ thực thi (Executor) độc lập tương ứng:
///       * "send": Gửi email qua Dynamic Mail Pool (nằm tại `send.rs`).
///

pub async fn dispatch_mail_job(
    action: &str,
    payload: JobPayload,
    _worker_pool: Arc<crate::workerpool::lifecycle::WorkerLifecycleManager>,
    redis_mgr: Arc<crate::infra::redis::RedisClientManager>,
    zone_id: &str,
) -> Result<ExecutionResult, ExecutorError> {
    Logger::sys_info(
        "executor.mail.router",
        &format!(
            "Mail Workload Router: Routing action '{}' for job_id={}",
            action, payload.job_id
        ),
    );

    match action {
        // Gửi email nghiệp vụ hoặc email hệ thống thông qua MailSendExecutor
        action if action == "send" || action.starts_with("system.") => {
            let exec = super::send::MailSendExecutor::new(
                redis_mgr,
                zone_id.to_string(),
            );
            exec.execute(payload).await
        }

        // Gặp hành động không được hỗ trợ
        _ => Err(ExecutorError::ExecutionFailed(format!(
            "Unsupported mail action: {}",
            action
        ))),
    }
}
