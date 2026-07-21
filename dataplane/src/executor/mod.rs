pub mod hypervisor;
pub mod mail;
pub mod storage;

// Sử dụng JobPayload từ module job_lifecycle mới đổi tên
use crate::job_lifecycle::message::JobPayload;
use async_trait::async_trait;

/// ============================================================================
/// 📂 MODULE: executor/mod.rs - Giao Diện & Bộ Khung Thực Thi Nghiệp Vụ
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Định nghĩa Trait chuẩn `Executor` cho toàn bộ các nghiệp vụ chạy trên Dataplane.
///   - Khai báo các cấu trúc lỗi (`ExecutorError`) và kết quả (`ExecutionResult`) chuẩn hóa.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Định nghĩa hợp đồng nghiệp vụ (Interface Contract) duy nhất của toàn bộ hệ thống thực thi.
///
/// 🔒 RANH GIỚI BẢO MẬT (PRIVACY BOUNDARY):
///   - Trait `Executor` chỉ tiếp nhận `JobPayload` chứa các thông số hạ tầng cấp thấp (non-tenant).
///   - Tuyệt đối phân tách luồng xử lý và dữ liệu của các workload khác nhau (Hypervisor độc lập với Mail).
///
/// 🔄 CALLSITE FLOW:
///   - Được kế thừa bởi toàn bộ các module nghiệp vụ cụ thể nằm trong `/hypervisor/` hoặc `/mail/`.
///   - Được gọi bởi `job-receiver/consumer.rs` sau khi giải mã và định tuyến job thành công.
///
/// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
///   - Mọi Executor khi triển khai bắt buộc phải tuân thủ nghiêm ngặt hai quy tắc:
///     1. **Idempotency (Tính duy nhất)**: Phải kiểm tra trùng lặp bản ghi trước khi thay đổi trạng thái hạ tầng.
///     2. **Deadline Enforcement (Thời hạn chờ)**: Phải tự hủy và Rollback nếu thời gian thực thi vượt quá timeout cho phép.
///
#[derive(Debug)]
pub enum ExecutorError {
    // Để tinh giản mã nguồn và loại bỏ cảnh báo biên dịch (dead_code), chúng ta chỉ giữ lại lỗi ExecutionFailed.
    // IdempotencyViolation và DeadlineExceeded tạm thời được lược bỏ vì:
    // 1. Logic Idempotency được xử lý thông qua database filter và silent success (trả về Ok).
    // 2. Deadline được xử lý trực tiếp bởi lớp bảo vệ Watchdog (Timeout) ở tầng ngoài.
    /// Các lỗi phát sinh trong quá trình tương tác API hoặc lỗi vật lý của máy chủ ảo hóa.
    ExecutionFailed(String),

    /// [COMMENT]: Hạ tầng tạm thời chưa sẵn sàng; Redis Stream entry phải ở lại PEL để pod claim và thử lại.
    Retryable(String),
}

/// Cấu trúc kết quả trả về sau khi thực thi nghiệp vụ hoàn tất.
pub struct ExecutionResult {
    // Loại bỏ các trường 'success' và 'return_code' vì chúng không được sử dụng ở tầng truyền kết quả
    // qua Redis Stream lên Controlplane. Việc hardcode "SUCCEEDED" / "FAILED" trên tầng cao giúp
    // đơn giản hóa và tăng tính an toàn dữ liệu.
    /// Chuỗi thông báo kỹ thuật mô tả kết quả xử lý.
    pub message: String,
}

#[async_trait]
pub trait Executor {
    /// Hàm thực thi nghiệp vụ cốt lõi. Mọi module nghiệp vụ mới bắt buộc phải cài đặt phương thức này.
    ///
    /// # Ràng buộc (Contract Invariants):
    ///   - Trả về `Result<ExecutionResult, ExecutorError>` rõ ràng.
    ///   - Không được phép để xảy ra tình trạng crash thô (panic) làm sập hệ thống Dataplane.
    async fn execute(&self, payload: JobPayload) -> Result<ExecutionResult, ExecutorError>;
}
