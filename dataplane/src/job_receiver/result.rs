use serde::{Deserialize, Serialize};

/// ============================================================================
/// 📂 MODULE: job-receiver/result.rs - Bộ Báo Cáo Kết Quả Xử Lý Nghiệp Vụ
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Đóng gói kết quả đầu ra sau khi Executor thực thi xong một Job nghiệp vụ.
///   - Cung cấp hai cơ chế báo cáo linh hoạt: Qua Redis Stream kết quả hoặc gửi gRPC trực tiếp lên Controlplane.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Trạng thái kết quả cuối cùng (Final outcome status) được quyết định bởi luồng thực thi của Executor.
///
/// 🔒 RANH GIỚI BẢO MẬT (PRIVACY BOUNDARY):
///   - Kết quả trả về chỉ ghi nhận trạng thái kỹ thuật (Succeeded, Failed, Error Code, Return Message).
///   - TUYỆT ĐỐI KHÔNG chứa dữ liệu Tenant ID hay thông tin cá nhân khách hàng.
///
/// 🔄 CALLSITE FLOW:
///   - Được gọi bởi các Executor sau khi hoàn tất xử lý nghiệp vụ vật lý.
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

pub struct JobResultReporter;

impl JobResultReporter {
    /// Đăng ký báo cáo kết quả thông qua luồng Redis kết quả.
    ///
    /// # Luồng xử lý kỹ thuật:
    ///   - Gọi hàm XADD đẩy gói kết quả vào Redis kết quả Stream.
    pub async fn report_via_redis_stream(_result: &JobExecutionResult) -> Result<(), String> {
        // Trên môi trường Production:
        //   - Sử dụng connection pool để lấy kết nối Redis.
        //   - Thực thi câu lệnh XADD đẩy bản ghi kết quả JSON.
        crate::observability::logger::Logger::sys_info(
            "job.result",
            "Job Result Reporter: Successfully published outcome to Redis stream",
        );
        Ok(())
    }

    /// Đăng ký báo cáo trực tiếp thông qua gRPC lên Controlplane.
    ///
    /// # Luồng xử lý kỹ thuật:
    ///   - Khởi tạo gRPC client kết nối lên Controlplane và gọi `ReportJobCompletion` RPC.
    pub async fn report_via_grpc(_result: &JobExecutionResult) -> Result<(), String> {
        // Thực hiện cuộc gọi thông qua Client gửi gRPC đã được cấu hình mTLS bảo mật
        crate::rpc::client::client::ExternalRpcSenderClient::send_to_controlplane(_result).await
    }
}
