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
}

impl JobExecutionResult {
    /// Dịch kết quả thô của luồng thực thi (gồm cả Timeout) thành cấu trúc kết quả nghiệp vụ.
    pub fn from_outcome(
        job_id: String,
        job_version: u32,
        attempt: u32,
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
            },
            Ok(Err(e)) => {
                let (code, msg) = match e {
                    crate::executor::ExecutorError::IdempotencyViolation(m) => {
                        (Some("IDEMPOTENCY_VIOLATION".to_string()), m)
                    }
                    crate::executor::ExecutorError::DeadlineExceeded(m) => {
                        (Some("DEADLINE_EXCEEDED".to_string()), m)
                    }
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
                    message: msg,
                }
            }
            Err(_) => Self {
                job_id,
                job_version,
                attempt,
                result_status: "FAILED".to_string(),
                error_code: Some("EXECUTION_TIMEOUT".to_string()),
                message: "Workload execution exceeded maximum execution time limit".to_string(),
            },
        }
    }
}

/// Tên stream mặc định để gửi kết quả xử lý Job cho Job-Proxy đọc.
/// Job-Proxy sẽ XREADGROUP từ stream này và cập nhật outbox table trong PSQL.
const DEFAULT_RESULT_STREAM: &str = "job_results_stream";

pub struct JobResultReporter;

impl JobResultReporter {
    /// Đẩy kết quả xử lý Job vào Redis Stream (durable delivery).
    ///
    /// Stream key: `job_results_stream` (cấu hình chung, Job-Proxy đọc từ cùng key).
    /// Payload: JSON serialized `JobExecutionResult` được lưu trong field `data`.
    ///
    /// # Race condition note:
    ///   - XADD là atomic trên Redis → không xảy ra race condition giữa nhiều Dataplane node.
    ///   - Job-Proxy consumer group đảm bảo mỗi message chỉ được xử lý bởi 1 consumer instance.
    pub async fn report_outcome(
        redis_client: &redis::Client,
        result: &JobExecutionResult,
    ) -> Result<(), String> {
        // Serialize kết quả thành JSON string để lưu vào Redis Stream field
        let payload_str = serde_json::to_string(result)
            .map_err(|e| format!("Failed to serialize job result to JSON: {}", e))?;

        // Đẩy vào Redis Stream bằng XADD (durable, persistent trên disk)
        let mut conn = redis_client
            .get_multiplexed_async_connection()
            .await
            .map_err(|e| format!("Failed to get Redis connection for result stream: {}", e))?;

        let _: String = redis::cmd("XADD")
            .arg(DEFAULT_RESULT_STREAM)
            .arg("*") // Auto-generate message ID
            .arg("data")
            .arg(&payload_str)
            .query_async(&mut conn)
            .await
            .map_err(|e| format!("Failed to XADD result to stream '{}': {}", DEFAULT_RESULT_STREAM, e))?;

        crate::observability::logger::Logger::sys_info(
            "job.result",
            &format!(
                "Job Result Reporter: XADD result to stream '{}' for job {} [status={}]",
                DEFAULT_RESULT_STREAM, result.job_id, result.result_status
            ),
        );

        Ok(())
    }
}
