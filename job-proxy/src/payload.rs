use serde::{Deserialize, Serialize};

/// JobPayload định nghĩa cấu trúc gói tin công việc được phân phối sang Dataplane.
/// Cấu trúc này bắt buộc phải khớp 100% với Deserializer của Dataplane.
#[derive(Serialize, Deserialize, Clone, Debug)]
pub struct JobPayload {
    /// Mã định danh duy nhất của Job
    pub job_id: String,

    /// Phiên bản của Job (dùng để kiểm soát xung đột ghi đè)
    pub job_version: u32,

    /// Số lần thử lại hiện tại
    pub attempt: u32,

    /// Chủ đề/Loại công việc cần xử lý (ví dụ: "mail.send")
    pub job_topic: String,

    /// Mã tài nguyên chịu tác động của Job
    pub resource_id: String,

    /// Phiên bản thiết kế của cấu trúc payload bên dưới
    pub payload_schema_version: u32,

    /// Dữ liệu kỹ thuật chi tiết dưới dạng nhị phân (Protobuf bytes)
    pub payload: Vec<u8>,

    /// Mã định danh vết xử lý để liên kết trace log
    pub trace_id: String,

    /// Hạn mức thời gian chạy tối đa (giây) của công việc
    pub idle: Option<u32>,
}

/// JobExecutionResult đại diện cho kết quả xử lý công việc nhận về từ Dataplane.
#[derive(Serialize, Deserialize, Clone, Debug)]
pub struct JobExecutionResult {
    /// Mã định danh duy nhất của Job
    pub job_id: String,

    /// Phiên bản của Job
    pub job_version: u32,

    /// Số lần thử lại thực tế
    pub attempt: u32,

    /// Trạng thái xử lý cuối cùng: "SUCCEEDED" | "FAILED" | "CANCELLED"
    pub result_status: String,

    /// Mã lỗi phân loại (nếu có)
    pub error_code: Option<String>,

    /// Chuỗi thông báo mô tả chi tiết kết quả xử lý
    pub message: String,
}

