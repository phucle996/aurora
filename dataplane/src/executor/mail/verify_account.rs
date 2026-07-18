use super::core::cache::get_template;
use super::core::render::render_template;
use super::core::send::send_raw_email;
use super::core::send::LmtpConnectionPool;
use super::send::mail_proto;
use crate::executor::{ExecutionResult, Executor, ExecutorError};
use crate::infra::redis::RedisClientManager;
use crate::job_lifecycle::message::JobPayload;
use crate::observability::logger::Logger;
use async_trait::async_trait;
use prost::Message;
use std::sync::Arc;

/// ============================================================================
/// 📂 BỘ THỰC THI GỬI EMAIL KÍCH HOẠT TÀI KHOẢN (PLATFORM WORKLOAD)
/// ============================================================================
pub struct MailVerifyAccountExecutor {
    redis_mgr: Arc<RedisClientManager>,
    _zone_id: String,
    lmtp_pool: Arc<LmtpConnectionPool>,
}

impl MailVerifyAccountExecutor {
    /// Khởi tạo MailVerifyAccountExecutor mới
    pub fn new(
        redis_mgr: Arc<RedisClientManager>,
        zone_id: String,
        lmtp_pool: Arc<LmtpConnectionPool>,
    ) -> Self {
        Self {
            redis_mgr,
            _zone_id: zone_id,
            lmtp_pool,
        }
    }
}

#[async_trait]
impl Executor for MailVerifyAccountExecutor {
    /// Thực thi nghiệp vụ gửi mail kích hoạt tài khoản bằng lõi core module
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

        // Lấy template_id từ variables truyền qua Outbox (yêu cầu bắt buộc phải có, không fallback)
        let template_id = mail_config
            .template_variables
            .get("template_id")
            .map(|s| s.as_str())
            .ok_or_else(|| {
                ExecutorError::ExecutionFailed(
                    "Thiếu tham số bắt buộc 'template_id' trong template_variables".to_string(),
                )
            })?;

        // 2. Lấy nội dung template dạng MailTemplate (chứa subject và body) qua cơ chế core::cache (L1 -> L2 -> PubSub)
        let template = get_template(&self.redis_mgr, template_id)
            .await
            .map_err(|e| {
                ExecutorError::ExecutionFailed(format!("Truy xuất template thất bại: {}", e))
            })?;

        // 3. Render tiêu đề (subject) và nội dung (body) bằng cách thế biến dynamic
        let subject = render_template(&template.subject, &mail_config.template_variables);
        let html_body = render_template(&template.body, &mail_config.template_variables);

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
            "executor.mail.verify",
            // [COMMENT]: Không log recipient để email không đi vào centralized operational logs.
            "Bắt đầu gửi verify email thông qua Stalwart LMTP Connection Pool...",
        );

        // 4. Gửi email đã render qua core::send::send_raw_email
        match send_raw_email(
            &self.lmtp_pool,
            sender_addr,
            recipient,
            &subject,
            &html_body,
        )
        .await
        {
            Ok(success_msg) => Ok(ExecutionResult {
                message: format!("Verify email LMTP delivery succeeded: {}", success_msg),
            }),
            Err(e) => Err(ExecutorError::ExecutionFailed(e)),
        }
    }
}
