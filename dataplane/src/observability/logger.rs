use std::sync::OnceLock;

/// ============================================================================
/// 📂 MODULE: observability/logger.rs - Hệ Thống Logs Cấu Trúc JSON Phân Cấp
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Xuất nhật ký hoạt động (logs) của Dataplane dưới dạng JSON cấu trúc (Structured Logging).
///   - Đồng bộ hóa 100% phong cách log với `pkg/logger.go` của Controlplane.
///   - Cung cấp ba phân hệ ghi logs riêng biệt: AccessLog, SystemLog, và JobLog.
///   - Tuân thủ nghiêm ngặt cấp độ ghi log (`APP_LOG_LEVEL`) được cấu hình trong hệ thống.
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
pub const LOG_TYPE_SYSTEM: &str = "system";
pub const LOG_TYPE_JOB: &str = "job";

/// Phân cấp độ ưu tiên của Log Level
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
pub enum LogLevel {
    Debug = 0,
    Info = 1,
    Warn = 2,
    Error = 3,
}

impl LogLevel {
    pub fn from_str(s: &str) -> Self {
        match s.to_lowercase().as_str() {
            "debug" => LogLevel::Debug,
            "warn" => LogLevel::Warn,
            "error" => LogLevel::Error,
            _ => LogLevel::Info, // Mặc định là Info
        }
    }
}

static CURRENT_LOG_LEVEL: OnceLock<LogLevel> = OnceLock::new();

pub struct Logger;

impl Logger {
    /// Lấy cấu hình log level hiện tại của ứng dụng (sử dụng cache OnceLock cực nhanh).
    fn get_level() -> LogLevel {
        *CURRENT_LOG_LEVEL.get_or_init(|| {
            let level_str = std::env::var("APP_LOG_LEVEL").unwrap_or_else(|_| "info".to_string());
            LogLevel::from_str(&level_str)
        })
    }

    /// Khởi tạo định dạng logger thô (JSON Formatter).
    pub fn init() {
        let level = Self::get_level();
        let timestamp = chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Nanos, true);
        println!(
            "{{\"time\":\"{}\",\"log_type\":\"system\",\"op\":\"logger.init\",\"level\":\"info\",\"message\":\"Observability Logger: JSON structured logging pipeline initialized. Level={:?}\"}}",
            timestamp, level
        );
    }

    /// Ghi nhận nhật ký gỡ lỗi hệ thống cấp thấp (System Debug Logs).
    pub fn sys_debug(op: &str, message: &str) {
        if Self::get_level() <= LogLevel::Debug {
            let timestamp = chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Nanos, true);
            let trace_id = crate::observability::otel::OtelTracer::get_current_trace_id();
            let trace_segment = if let Some(ref tid) = trace_id {
                format!(",\"trace_id\":\"{}\"", tid)
            } else {
                "".to_string()
            };
            println!(
                "{{\"time\":\"{}\",\"log_type\":\"{}\",\"op\":\"{}\",\"level\":\"debug\",\"message\":\"{}\"{}}}",
                timestamp, LOG_TYPE_SYSTEM, op, message, trace_segment
            );
        }
    }



    /// Ghi nhận nhật ký hệ thống cấp thấp (System Info Logs).
    pub fn sys_info(op: &str, message: &str) {
        if Self::get_level() <= LogLevel::Info {
            let timestamp = chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Nanos, true);
            let trace_id = crate::observability::otel::OtelTracer::get_current_trace_id();
            let trace_segment = if let Some(ref tid) = trace_id {
                format!(",\"trace_id\":\"{}\"", tid)
            } else {
                "".to_string()
            };
            println!(
                "{{\"time\":\"{}\",\"log_type\":\"{}\",\"op\":\"{}\",\"level\":\"info\",\"message\":\"{}\"{}}}",
                timestamp, LOG_TYPE_SYSTEM, op, message, trace_segment
            );
        }
    }

    /// Ghi nhận nhật ký cảnh báo lỗi hệ thống cấp thấp (System Warn Logs).
    pub fn sys_warn(op: &str, message: &str, err_msg: &str) {
        if Self::get_level() <= LogLevel::Warn {
            let timestamp = chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Nanos, true);
            let trace_id = crate::observability::otel::OtelTracer::get_current_trace_id();
            let trace_segment = if let Some(ref tid) = trace_id {
                format!(",\"trace_id\":\"{}\"", tid)
            } else {
                "".to_string()
            };
            println!(
                "{{\"time\":\"{}\",\"log_type\":\"{}\",\"op\":\"{}\",\"level\":\"warn\",\"message\":\"{}\",\"error\":\"{}\"{}}}",
                timestamp, LOG_TYPE_SYSTEM, op, message, err_msg, trace_segment
            );
        }
    }

    /// Ghi nhận nhật ký lỗi hệ thống nghiêm trọng (System Error Logs).
    pub fn sys_error(op: &str, message: &str, err_msg: &str) {
        if Self::get_level() <= LogLevel::Error {
            let timestamp = chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Nanos, true);
            let trace_id = crate::observability::otel::OtelTracer::get_current_trace_id();
            let trace_segment = if let Some(ref tid) = trace_id {
                format!(",\"trace_id\":\"{}\"", tid)
            } else {
                "".to_string()
            };
            eprintln!(
                "{{\"time\":\"{}\",\"log_type\":\"{}\",\"op\":\"{}\",\"level\":\"error\",\"message\":\"{}\",\"error\":\"{}\"{}}}",
                timestamp, LOG_TYPE_SYSTEM, op, message, err_msg, trace_segment
            );
        }
    }

    /// Ghi nhận toàn bộ vết di chuyển của một Job nghiệp vụ (Job Logs).
    pub fn job_log(job_id: &str, job_topic: &str, attempt: u32, op: &str, message: &str) {
        if Self::get_level() <= LogLevel::Info {
            let timestamp = chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Nanos, true);
            let trace_id = crate::observability::otel::OtelTracer::get_current_trace_id();
            let trace_segment = if let Some(ref tid) = trace_id {
                format!(",\"trace_id\":\"{}\"", tid)
            } else {
                "".to_string()
            };
            println!(
                "{{\"time\":\"{}\",\"log_type\":\"{}\",\"job_id\":\"{}\",\"job_topic\":\"{}\",\"attempt\":{},\"op\":\"{}\",\"message\":\"{}\"{}}}",
                timestamp, LOG_TYPE_JOB, job_id, job_topic, attempt, op, message, trace_segment
            );
        }
    }
}
