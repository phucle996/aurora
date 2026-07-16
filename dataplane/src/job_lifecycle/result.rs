use serde::{Deserialize, Serialize};

/// ============================================================================
/// 📂 MODULE: job-receiver/result.rs - Bộ Báo Cáo Kết Quả Xử Lý Nghiệp Vụ
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Đóng gói kết quả đầu ra sau khi Executor thực thi xong một Job nghiệp vụ.
///   - Báo cáo kết quả qua Redis Stream (durable) để Job-Proxy cập nhật outbox table trong PostgreSQL.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Trạng thái kết quả cuối cùng (Final outcome status) được quyết định bởi luồng thực thi của Executor.
///
/// 🔒 RANH GIỚI BẢO MẬT (PRIVACY BOUNDARY):
///   - Kết quả trả về chỉ ghi nhận trạng thái kỹ thuật (Succeeded, Failed, Error Code, Return Message).
///   - TUYỆT ĐỐI KHÔNG chứa dữ liệu Tenant ID hay thông tin cá nhân khách hàng.
///
/// 🔄 CALLSITE FLOW:
///   - Được gọi bởi Worker (`runner.rs`) và Watchdog (`watchdog.rs`) để đẩy kết quả vào Redis Stream.
///   - Job-Proxy đọc stream → cập nhật `outbox_records` trong PostgreSQL → XACK.
///
/// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
///   - Sử dụng Redis Stream thay vì Pub/Sub để đảm bảo durability:
///     + Job-Proxy restart → resume từ last ACK, không mất message.
///     + Exactly-once delivery với XACK consumer group.
///
pub mod job_proto {
    include!(concat!(env!("OUT_DIR"), "/job_lifecycle.rs"));
}

#[derive(Serialize, Deserialize, Clone, Debug)]
pub struct JobExecutionResult {
    /// Mã định danh duy nhất của Job nghiệp vụ được xử lý.
    pub job_id: String,

    /// Phiên bản của Job (để so sánh tính nhất quán).
    pub job_version: u32,

    /// Số lần thử lại thực tế của Job này.
    pub attempt: u32,

    /// Trạng thái xử lý cuối cùng: "SUCCEEDED" | "FAILED" | "PROCESSING".
    pub result_status: String,

    /// Mã lỗi kỹ thuật phân loại cụ thể (nếu có). Ví dụ: "INSUFFICIENT_RESOURCE".
    pub error_code: Option<String>,

    /// Chuỗi thông báo mô tả chi tiết kết quả xử lý thực tế phục vụ gỡ lỗi (debugging).
    pub message: String,

    /// Tên topic sự kiện (e.g. "mail.test_connection")
    pub job_topic: String,

    /// OpenTelemetry trace id (32 ký tự hex)
    pub trace_id: String,
}

impl JobExecutionResult {
    /// Dịch kết quả thô của luồng thực thi (gồm cả Timeout) thành cấu trúc kết quả nghiệp vụ.
    pub fn from_outcome(
        job_id: String,
        job_version: u32,
        attempt: u32,
        job_topic: String,
        trace_id: String,
        outcome: Result<
            Result<crate::executor::ExecutionResult, crate::executor::ExecutorError>,
            tokio::time::error::Elapsed,
        >,
    ) -> Self {
        match outcome {
            Ok(Ok(res)) => Self {
                job_id,
                job_version,
                attempt,
                result_status: "SUCCEEDED".to_string(),
                error_code: None,
                message: res.message,
                job_topic,
                trace_id,
            },
            Ok(Err(e)) => {
                // Do chúng ta đã rút gọn ExecutorError chỉ còn variant ExecutionFailed để xóa dead code,
                // phần match lỗi ở đây được đơn giản hóa để chỉ ánh xạ duy nhất lỗi này về Controlplane.
                let (code, _msg) = match e {
                    crate::executor::ExecutorError::ExecutionFailed(m) => {
                        (Some("EXECUTION_FAILED".to_string()), m)
                    }
                };
                Self {
                    job_id,
                    job_version,
                    attempt,
                    result_status: "FAILED".to_string(),
                    error_code: code,
                    message: "".to_string(),
                    job_topic,
                    trace_id,
                }
            }
            Err(_) => Self {
                job_id,
                job_version,
                attempt,
                result_status: "FAILED".to_string(),
                error_code: Some("EXECUTION_TIMEOUT".to_string()),
                message: "".to_string(),
                job_topic,
                trace_id,
            },
        }
    }
}

/// Tên stream mặc định để gửi kết quả xử lý Job cho Job-Proxy đọc.
/// Job-Proxy sẽ XREADGROUP từ stream này và cập nhật outbox table trong PSQL.
const DEFAULT_RESULT_STREAM: &str = "job_results_stream";

// Helper giải mã chuỗi hex sang mảng byte nhị phân thô
fn decode_hex(s: &str) -> Vec<u8> {
    let mut bytes = Vec::new();
    let mut chars = s.chars();
    while let (Some(c1), Some(c2)) = (chars.next(), chars.next()) {
        if let Some(b) = hex_chars_to_byte(c1, c2) {
            bytes.push(b);
        }
    }
    bytes
}

fn hex_chars_to_byte(c1: char, c2: char) -> Option<u8> {
    let n1 = c1.to_digit(16)?;
    let n2 = c2.to_digit(16)?;
    Some((n1 << 4 | n2) as u8)
}

pub struct JobResultReporter;

impl JobResultReporter {
    /// Đẩy kết quả xử lý Job vào Redis Stream dưới dạng Protobuf nhị phân tối ưu.
    ///
    /// Stream key: `job_results_stream` (cấu hình chung, Job-Proxy đọc từ cùng key).
    /// Payload: Protobuf serialized `JobExecutionResultProto` được lưu trong field `payload`.
    pub async fn report_outcome(
        redis_client: &redis::Client,
        result: &JobExecutionResult,
    ) -> Result<(), String> {
        use prost::Message;

        // Parse UUID string thành 16 bytes nhị phân
        let job_id_bytes = uuid::Uuid::parse_str(&result.job_id)
            .map(|u| u.as_bytes().to_vec())
            .unwrap_or_default();

        // Convert hex trace_id thành 16 bytes nhị phân
        let trace_id_bytes = if result.trace_id.is_empty() {
            Vec::new()
        } else {
            decode_hex(&result.trace_id)
        };

        // Khởi tạo Protobuf payload tương thích với schema
        let proto_msg = job_proto::JobExecutionResultProto {
            job_id: job_id_bytes,
            job_version: result.job_version,
            attempt: result.attempt,
            result_status: result.result_status.clone(),
            job_topic: result.job_topic.clone(),
            trace_id: trace_id_bytes,
            error_code: result.error_code.clone(),
            message: result.message.clone(),
        };

        let mut buf = Vec::new();
        proto_msg.encode(&mut buf)
            .map_err(|e| format!("Failed to serialize job result to Protobuf: {}", e))?;

        // Đẩy vào Redis Stream bằng XADD (durable, persistent trên disk)
        let mut conn = redis_client
            .get_multiplexed_async_connection()
            .await
            .map_err(|e| format!("Failed to get Redis connection for result stream: {}", e))?;

        // Dùng field "payload" để truyền tải array bytes nhị phân trực tiếp
        let _: String = redis::cmd("XADD")
            .arg(DEFAULT_RESULT_STREAM)
            .arg("*") // Auto-generate message ID
            .arg("payload")
            .arg(&buf)
            .query_async(&mut conn)
            .await
            .map_err(|e| {
                format!(
                    "Failed to XADD Protobuf result to stream '{}': {}",
                    DEFAULT_RESULT_STREAM, e
                )
            })?;

        crate::observability::logger::Logger::sys_info(
            "job.result",
            &format!(
                "Job Result Reporter (Protobuf): XADD payload to stream '{}' for job {} [status={}]",
                DEFAULT_RESULT_STREAM, result.job_id, result.result_status
            ),
        );

        Ok(())
    }
}
