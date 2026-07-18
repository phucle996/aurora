use super::core::send::{send_raw_email, LmtpConnectionPool};
use crate::executor::{ExecutionResult, Executor, ExecutorError};
use crate::infra::redis::RedisClientManager;
use crate::job_lifecycle::message::JobPayload;
use crate::observability::logger::Logger;
use async_trait::async_trait;
use prost::Message;
use std::sync::Arc;

pub mod mail_proto {
    include!(concat!(env!("OUT_DIR"), "/mail.rs"));
}

/// ============================================================================
/// 📂 BỘ THỰC THI GỬI EMAIL THÔNG QUA STALWART LMTP CONNECTION POOL
/// ============================================================================
pub struct MailSendExecutor {
    _redis_mgr: Arc<RedisClientManager>,
    _zone_id: String,
    lmtp_pool: Arc<LmtpConnectionPool>,
}

impl MailSendExecutor {
    /// Khởi tạo một đối tượng MailSendExecutor mới tích hợp Connection Pool
    pub fn new(
        redis_mgr: Arc<RedisClientManager>,
        zone_id: String,
        lmtp_pool: Arc<LmtpConnectionPool>,
    ) -> Self {
        Self {
            _redis_mgr: redis_mgr,
            _zone_id: zone_id,
            lmtp_pool,
        }
    }
}

#[async_trait]
impl Executor for MailSendExecutor {
    /// Thực thi nghiệp vụ gửi mail transactional bằng cách tái sử dụng kết nối LMTP từ pool
    async fn execute(&self, payload: JobPayload) -> Result<ExecutionResult, ExecutorError> {
        // 1. Giải mã cấu hình gửi email từ Protobuf payload
        let mail_config = match mail_proto::SendMailConfig::decode(payload.payload.as_slice()) {
            Ok(c) => c,
            Err(e) => {
                return Err(ExecutorError::ExecutionFailed(format!(
                    "Lỗi giải mã SendMailConfig: {}",
                    e
                )));
            }
        };

        // Lấy địa chỉ người gửi (sender) từ template_variables (yêu cầu bắt buộc)
        let sender_addr = mail_config
            .template_variables
            .get("from")
            .or_else(|| mail_config.template_variables.get("sender"))
            .map(|s| s.as_str())
            .ok_or_else(|| {
                ExecutorError::ExecutionFailed(
                    "Thiếu tham số bắt buộc 'from' hoặc 'sender' trong template_variables"
                        .to_string(),
                )
            })?;

        // Lấy địa chỉ người nhận (recipient) từ template_variables (yêu cầu bắt buộc)
        let recipient = mail_config
            .template_variables
            .get("to")
            .map(|s| s.as_str())
            .ok_or_else(|| {
                ExecutorError::ExecutionFailed(
                    "Thiếu tham số bắt buộc 'to' trong template_variables".to_string(),
                )
            })?;

        Logger::sys_info(
            "executor.mail.send",
            // [COMMENT]: Recipient là PII; correlation dùng job_id ở lifecycle log thay vì địa chỉ email.
            "Bắt đầu gửi email thông qua Stalwart LMTP Connection Pool...",
        );

        // Lấy tiêu đề email (subject) từ template_variables (yêu cầu bắt buộc)
        let subject = mail_config
            .template_variables
            .get("subject")
            .map(|s| s.as_str())
            .ok_or_else(|| {
                ExecutorError::ExecutionFailed(
                    "Thiếu tham số bắt buộc 'subject' trong template_variables".to_string(),
                )
            })?;

        // Lấy nội dung email (body) từ template_variables (yêu cầu bắt buộc)
        let body_html = mail_config
            .template_variables
            .get("body")
            .or_else(|| mail_config.template_variables.get("body_html"))
            .map(|s| s.as_str())
            .ok_or_else(|| {
                ExecutorError::ExecutionFailed(
                    "Thiếu tham số bắt buộc 'body' hoặc 'body_html' trong template_variables"
                        .to_string(),
                )
            })?;

        // 2. Gọi hàm send_raw_email từ core module để chuyển tiếp
        match send_raw_email(&self.lmtp_pool, sender_addr, recipient, subject, body_html).await {
            Ok(success_msg) => Ok(ExecutionResult {
                message: format!("LMTP delivery succeeded: {}", success_msg),
            }),
            Err(e) => Err(ExecutorError::ExecutionFailed(e)),
        }
    }
}
