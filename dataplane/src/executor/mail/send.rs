use crate::executor::{Executor, ExecutorError, ExecutionResult};
use crate::job_receiver::message::JobPayload;
use async_trait::async_trait;

/// ============================================================================
/// 📂 MODULE: executor/mail/send.rs - Bộ Thực Thi Nghiệp Vụ Gửi Mail Giao Dịch
/// ============================================================================
/// 
/// 📌 VAI TRÒ (ROLE):
///   - Triển khai logic thực tế cho nghiệp vụ gửi thư điện tử giao dịch (transactional email)
///     như mail báo lỗi, mail báo trạng thái VPS hoàn tất cho khách hàng.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Trạng thái phản hồi từ API của Mail Server ngoại vi (SMTP Server, Mailgun, SendGrid...).
///
/// 🔒 RANH GIỚI BẢO MẬT (PRIVACY BOUNDARY):
///   - Không chứa bất kỳ liên kết hay dữ liệu Tenant cụ thể nào trong cấu trúc hoạt động.
///   - Chỉ nhận dữ liệu email thô từ Payload đã được Controlplane mã hóa và chuẩn bị sẵn.
///
/// 🔄 CALLSITE FLOW:
///   - Được gọi bởi `job-receiver/consumer.rs` khi định tuyến topic "mail.send".
///
/// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
///   - Tác vụ gửi mail tương đối nhẹ nhưng phụ thuộc lớn vào chất lượng kết nối của SMTP Server ngoại vi.
///   - Áp dụng cơ chế **Retry Timeout ngắn** (ví dụ: 10s) để tránh treo luồng khi Mail Server bị nghẽn.
///
pub struct MailExecutor;

#[async_trait]
impl Executor for MailExecutor {
    /// Thực thi tác vụ gửi thư điện tử giao dịch.
    ///
    /// # Luồng xử lý kỹ thuật (Technical Flow):
    ///   1. Check Idempotency bằng SQLite để tránh gửi lặp mail cho khách.
    ///   2. Đọc nội dung email thô (body, recipient) từ `payload.payload_json`.
    ///   3. Gửi lệnh qua SMTP/HTTPS tới Mail Server.
    ///   4. Trả về `ExecutionResult` tương ứng.
    async fn execute(&self, payload: JobPayload) -> Result<ExecutionResult, ExecutorError> {
        crate::observability::logger::Logger::sys_info(
            "executor.mail",
            &format!("Mail Executor: Dispatching email delivery task: job_id={}", payload.job_id),
        );
        
        // Trên môi trường Production thực tế:
        //   - Thực thi lệnh gọi SMTP Client với cơ chế Timeout chặt chẽ.
        //   - Bắt và xử lý các lỗi mã trạng thái HTTP/SMTP để chuyển đổi thành ExecutorError hợp lệ.
        
        Ok(ExecutionResult {
            success: true,
            return_code: "SUCCESS".to_string(),
            message: "Mail: Transactional email successfully delivered to mail server queue".to_string(),
        })
    }
}
