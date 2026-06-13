use serde::{Deserialize, Serialize};

/// ============================================================================
/// 📂 MODULE: job-receiver/result.rs - Bộ Báo Cáo Kết Quả Xử Lý Nghiệp Vụ
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Đóng gói kết quả đầu ra sau khi Executor thực thi xong một Job nghiệp vụ.
///   - Cung cấp các cơ chế báo cáo linh hoạt: Qua Redis Stream, Redis Pub/Sub, hoặc gửi gRPC trực tiếp lên Controlplane.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Trạng thái kết quả cuối cùng (Final outcome status) được quyết định bởi luồng thực thi của Executor.
///
/// 🔒 RANH GIỚI BẢO MẬT (PRIVACY BOUNDARY):
///   - Kết quả trả về chỉ ghi nhận trạng thái kỹ thuật (Succeeded, Failed, Error Code, Return Message).
///   - TUYỆT ĐỐI KHÔNG chứa dữ liệu Tenant ID hay thông tin cá nhân khách hàng.
///
/// 🔄 CALLSITE FLOW:
///   - Được gọi bởi Worker sau khi hoàn tất xử lý nghiệp vụ vật lý để đẩy báo cáo lên các kênh.
///
/// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
///   - Khi gửi qua gRPC, cần áp dụng thuật toán **Exponential Backoff với Jitter**
///     để đảm bảo tính bền bỉ (resilience retry) khi Controlplane chịu tải cao hoặc bị nghẽn mạng tạm thời.
///
#[derive(Serialize, Deserialize, Clone, Debug)]
pub struct JobExecutionResult {
    /// Mã định danh duy nhất của Job nghiệp vụ được xử lý.
    pub job_id: String,

    /// Phiên bản của Job (để so sánh tính nhất quán).
    pub job_version: u32,

    /// Số lần thử lại thực tế của Job này.
    pub attempt: u32,

    /// Trạng thái xử lý cuối cùng: "SUCCEEDED" | "FAILED" | "CANCELLED".
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

pub struct JobResultReporter;

impl JobResultReporter {
    /// Phát hành kết quả công việc lên kênh Redis Pub/Sub chuyên biệt để UI/API đón đầu phản hồi.
    pub async fn report_via_redis_pubsub(
        redis_client: &redis::Client,
        channel: &str,
        result: &JobExecutionResult,
    ) -> Result<(), String> {
        let payload_str = serde_json::to_string(result)
            .map_err(|e| format!("Failed to serialize job result to JSON: {}", e))?;

        crate::infra::redis::query::publish_pubsub(redis_client, channel, &payload_str)
            .await
            .map_err(|e| format!("Failed to publish via Redis Pub/Sub: {}", e))?;

        crate::observability::logger::Logger::sys_info(
            "job.result",
            &format!(
                "Job Result Reporter: Successfully published outcome to Redis Pub/Sub channel: {}",
                channel
            ),
        );
        Ok(())
    }

    /// Đăng ký báo cáo trực tiếp thông qua gRPC lên Controlplane.
    pub async fn report_via_grpc(_result: &JobExecutionResult) -> Result<(), String> {
        crate::rpc::client::client::ExternalRpcSenderClient::send_to_controlplane(_result).await
    }

    /// Hợp nhất tất cả các phương thức báo cáo (Redis Pub/Sub & gRPC) thành một cuộc gọi duy nhất.
    /// Giúp Worker giải phóng kết quả sạch sẽ mà không phân mảnh code.
    pub async fn report_outcome(
        redis_client: &redis::Client,
        pubsub_channel: &str,
        result: &JobExecutionResult,
    ) -> Result<(), String> {
        // 1. Gửi lên kênh Redis Pub/Sub để báo tin tức thời cho client/UI đang chờ
        let _ = Self::report_via_redis_pubsub(redis_client, pubsub_channel, result).await;

        // 2. Gửi gRPC lên Controlplane cập nhật trạng thái bền vững (SoT DB)
        let _ = Self::report_via_grpc(result).await;

        Ok(())
    }
}
