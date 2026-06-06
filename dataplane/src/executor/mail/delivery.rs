use crate::executor::{ExecutionResult, Executor, ExecutorError};
use crate::job_receiver::message::JobPayload;
use std::sync::Arc;

/// ============================================================================
/// 📂 MODULE: executor/mail/delivery.rs - BỘ PHÂN PHỐI WORKLOAD MAIL (GATEKEEPER)
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Đóng vai trò là công tắc định tuyến và cổng kiểm soát (Gatekeeper) ở cấp Workload Mail.
///   - Chịu trách nhiệm yêu cầu worker từ `WorkerLifecycleManager` để chạy tác vụ nặng.
///   - Nếu không xin được worker, lập tức trả về lỗi để đẩy trạng thái thất bại lên Redis.
///
pub async fn dispatch_mail_job(
    action: &str,
    payload: JobPayload,
    worker_pool: Arc<crate::workerpool::lifecycle::WorkerLifecycleManager>,
) -> Result<ExecutionResult, ExecutorError> {
    crate::observability::logger::Logger::sys_info(
        "executor.mail.router",
        &format!(
            "Mail Workload Router: Routing action '{}' for job_id={}",
            action, payload.job_id
        ),
    );

    match action {
        // Gửi email giao dịch thông thường
        "send" => {
            let exec = super::send::MailExecutor;
            exec.execute(payload).await
        }
        // Kiểm tra kết nối SMTP server (handshake + credentials validation)
        "test_connection" => {
            let exec = super::test_connection::SmtpTestExecutor;
            // workload mail sẽ tự lấy worker từ worker pool để thực thi action với payload (Giai đoạn 2)
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
