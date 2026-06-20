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
///       * "test_connection": Chạy kiểm thử kết nối SMTP trực tiếp trên threadpool (nằm tại `test_connection.rs`).
///       * "create_endpoint" | "sync_endpoint": Đồng bộ cấu hình Endpoint vật lý (nằm tại `sync_endpoint.rs`).
///       * "delete_endpoint": Xóa cấu hình Endpoint vật lý (nằm tại `delete_endpoint.rs`).
///

pub async fn dispatch_mail_job(
    action: &str,
    payload: JobPayload,
    worker_pool: Arc<crate::workerpool::lifecycle::WorkerLifecycleManager>,
    mail_server_pool: Arc<crate::executor::mail::registry::MailServerPool>,
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
                mail_server_pool,
                redis_mgr,
                zone_id.to_string(),
            );
            exec.execute(payload).await
        }

        // Kiểm tra kết nối SMTP server (yêu cầu worker từ WorkerLifecycleManager để chạy stateless check tránh blocking thread)
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

        // Đồng bộ cấu hình Endpoint vật lý thông qua SmtpSyncExecutor
        "create_endpoint" | "sync_endpoint" => {
            let exec = super::sync_endpoint::SmtpSyncExecutor::new(
                mail_server_pool,
                redis_mgr,
                zone_id.to_string(),
            );
            exec.execute(payload).await
        }

        // Xóa Endpoint vật lý khỏi hệ thống thông qua SmtpDeleteExecutor
        "delete_endpoint" => {
            let exec = super::delete_endpoint::SmtpDeleteExecutor::new(
                mail_server_pool,
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
