use crate::executor::{ExecutionResult, Executor, ExecutorError};
// Sử dụng JobPayload từ module job_lifecycle mới đổi tên
use crate::job_lifecycle::message::JobPayload;
use crate::observability::logger::Logger;
use std::sync::Arc;

/// ============================================================================
/// 📂 MODULE: executor/mail/delivery.rs - BỘ PHÂN PHỐI WORKLOAD MAIL (GATEKEEPER)
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Đóng vai trò là công tắc định tuyến và cổng kiểm soát (Gatekeeper) ở cấp Workload Mail.
///   - Nhận yêu cầu định tuyến của các hành động (Action) cụ thể thuộc Mail Workload.
///   - Gửi yêu cầu xin Worker từ Worker Pool cho các hành động nặng (ví dụ: test_connection).
///
pub async fn dispatch_mail_job(
    action: &str,
    payload: JobPayload,
    worker_pool: Arc<crate::workerpool::lifecycle::WorkerLifecycleManager>,
) -> Result<ExecutionResult, ExecutorError> {
    Logger::sys_info(
        "executor.mail.router",
        &format!(
            "Mail Workload Router: Routing action '{}' for job_id={}",
            action, payload.job_id
        ),
    );

    match action {
        // Gửi email giao dịch thông thường (chạy trực tiếp)
        "send" => {
            let exec = super::send::MailExecutor;
            exec.execute(payload).await
        }
        // Kiểm tra kết nối SMTP server (yêu cầu worker từ WorkerLifecycleManager)
        "test_connection" => {
            let exec = super::test_connection::SmtpTestExecutor;
            worker_pool
                .spawn(move || async move { exec.execute(payload).await })
                .await
                .map_err(|e| {
                    ExecutorError::ExecutionFailed(format!(
                        "Failed to acquire worker from pool: {}",
                        e
                    ))
                })?
        }
        // Gặp action không được định nghĩa
        _ => Err(ExecutorError::ExecutionFailed(format!(
            "Unsupported mail action: {}",
            action
        ))),
    }
}
