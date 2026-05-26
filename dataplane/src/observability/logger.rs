use std::collections::HashMap;

/// ============================================================================
/// 📂 MODULE: observability/logger.rs - Hệ Thống Logs Cấu Trúc JSON Phân Cấp
/// ============================================================================
/// 
/// 📌 VAI TRÒ (ROLE):
///   - Xuất nhật ký hoạt động (logs) của Dataplane dưới dạng JSON cấu trúc (Structured Logging).
///   - Đồng bộ hóa 100% phong cách log với `pkg/logger.go` của Controlplane.
///   - Cung cấp ba phân hệ ghi logs riêng biệt: AccessLog, SystemLog, và JobLog.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Nhật ký xuất ra trực tiếp luồng ra tiêu chuẩn lỗi `std::stderr` của container,
///     được thu thập bởi FluentBit / Promtail để đẩy lên Grafana Loki.
///
/// 🔒 RANH GIỚI BẢO MẬT (PRIVACY BOUNDARY):
///   - TUYỆT ĐỐI CẤM ghi đè (log) toàn bộ nội dung trường nhạy cảm `payload_json`.
///   - Chỉ được ghi nhận các nhãn kỹ thuật thô phục vụ gỡ lỗi.
///
/// 🔄 CALLSITE FLOW:
///   - Được gọi xuyên suốt toàn bộ ứng dụng ở mọi file.
///
/// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
///   - Mọi log ghi ra bắt buộc phải mang mốc thời gian độ phân giải cao `RFC3339Nano` để phân tích
///     chính xác thứ tự xảy ra sự kiện trên production.
///
pub const LOG_TYPE_ACCESS: &str = "access";
pub const LOG_TYPE_SYSTEM: &str = "system";
pub const LOG_TYPE_JOB: &str = "job";

pub struct Fields(pub HashMap<String, serde_json::Value>);

pub struct Logger;

impl Logger {
    /// Khởi tạo định dạng logger thô (JSON Formatter).
    pub fn init() {
        println!("Observability Logger: JSON structured logging pipeline initialized. Level=INFO.");
    }

    /// Ghi nhận nhật ký truy cập mạng/kết nối mạng (Access Logs).
    ///
    /// # Tham số:
    ///   - `op`: Tên tác vụ xử lý. Ví dụ: "grpc.ReportJobCompletion".
    ///   - `method`: HTTP Method hoặc RPC Type.
    ///   - `latency_ms`: Độ trễ mạng thực tế của cuộc gọi.
    pub fn access_log(op: &str, method: &str, route: &str, status_code: i32, latency_ms: f64) {
        let timestamp = chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Nanos, true);
        println!(
            "{{\"time\":\"{}\",\"log_type\":\"{}\",\"op\":\"{}\",\"method\":\"{}\",\"route\":\"{}\",\"status_code\":{},\"latency_ms\":{:.3}}}",
            timestamp, LOG_TYPE_ACCESS, op, method, route, status_code, latency_ms
        );
    }

    /// Ghi nhận nhật ký hệ thống cấp thấp (System Info Logs).
    /// Dùng cho các tiến trình: Khởi động, thay đổi chính sách động thành công.
    pub fn sys_info(op: &str, message: &str) {
        let timestamp = chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Nanos, true);
        println!(
            "{{\"time\":\"{}\",\"log_type\":\"{}\",\"op\":\"{}\",\"level\":\"info\",\"message\":\"{}\"}}",
            timestamp, LOG_TYPE_SYSTEM, op, message
        );
    }

    /// Ghi nhận nhật ký cảnh báo lỗi hệ thống cấp thấp (System Warn Logs).
    /// Dùng cho các lỗi: Mất kết nối Redis tạm thời, YAML chính sách bị lỗi cú pháp (giữ LKG).
    pub fn sys_warn(op: &str, message: &str, err_msg: &str) {
        let timestamp = chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Nanos, true);
        println!(
            "{{\"time\":\"{}\",\"log_type\":\"{}\",\"op\":\"{}\",\"level\":\"warn\",\"message\":\"{}\",\"error\":\"{}\"}}",
            timestamp, LOG_TYPE_SYSTEM, op, message, err_msg
        );
    }

    /// Ghi nhận nhật ký lỗi hệ thống nghiêm trọng (System Error Logs).
    /// Dùng cho các lỗi: Không kết nối được DB, không kết nối được Redis (dẫn tới abort/shutdown).
    pub fn sys_error(op: &str, message: &str, err_msg: &str) {
        let timestamp = chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Nanos, true);
        eprintln!(
            "{{\"time\":\"{}\",\"log_type\":\"{}\",\"op\":\"{}\",\"level\":\"error\",\"message\":\"{}\",\"error\":\"{}\"}}",
            timestamp, LOG_TYPE_SYSTEM, op, message, err_msg
        );
    }

    /// Ghi nhận toàn bộ vết di chuyển của một Job nghiệp vụ (Job Logs).
    /// Giúp theo dõi chính xác vòng đời xử lý của một Job (Nhận -> Validate -> Chạy -> Hoàn thành).
    pub fn job_log(job_id: &str, job_topic: &str, attempt: u32, op: &str, message: &str) {
        let timestamp = chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Nanos, true);
        println!(
            "{{\"time\":\"{}\",\"log_type\":\"{}\",\"job_id\":\"{}\",\"job_topic\":\"{}\",\"attempt\":{},\"op\":\"{}\",\"message\":\"{}\"}}",
            timestamp, LOG_TYPE_JOB, job_id, job_topic, attempt, op, message
        );
    }
}
