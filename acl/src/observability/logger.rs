use std::sync::OnceLock;

/// ============================================================================
/// 📂 MODULE: observability/logger.rs - Hệ Thống Logs Cấu Trúc JSON Phân Cấp
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Xuất nhật ký hoạt động (logs) của ACL Service dưới dạng JSON cấu trúc (Structured Logging).
///   - Đồng bộ hóa 100% phong cách log với `pkg/logger.go` của Controlplane
///     và `observability/logger.rs` của Dataplane.
///   - Cung cấp hai phân hệ ghi logs: SystemLog (vận hành) và AuthzLog (quyết định authorize).
///   - Tuân thủ nghiêm ngặt cấp độ ghi log (`APP_LOG_LEVEL`) được cấu hình trong hệ thống.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - Nhật ký xuất ra trực tiếp luồng ra tiêu chuẩn lỗi `std::stderr` của container,
///     được thu thập bởi FluentBit / Promtail để đẩy lên Grafana Loki.
///
/// 🔒 RANH GIỚI BẢO MẬT (PRIVACY BOUNDARY):
///   - TUYỆT ĐỐI CẤM ghi đè (log) toàn bộ nội dung JWT token, session secrets.
///   - Chỉ được ghi nhận các nhãn kỹ thuật thô phục vụ gỡ lỗi.
///
/// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
///   - Mọi log ghi ra bắt buộc phải mang mốc thời gian độ phân giải cao `RFC3339Nano`
///     để phân tích chính xác thứ tự xảy ra sự kiện trên production.
///

// Nhãn phân loại log
pub const LOG_TYPE_SYSTEM: &str = "system";
pub const LOG_TYPE_AUTHZ: &str = "authz";

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

// Cache cấp độ log toàn cục (OnceLock chỉ khởi tạo 1 lần, read cực nhanh)
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

    /// Tạo trace segment tùy theo trace_id hiện tại có tồn tại trong async context không
    fn trace_segment() -> String {
        if let Some(ref tid) = crate::observability::otel::OtelTracer::get_current_trace_id() {
            format!(",\"trace_id\":\"{}\"", tid)
        } else {
            "".to_string()
        }
    }

    /// Khởi tạo định dạng logger thô (JSON Formatter).
    pub fn init() {
        let level = Self::get_level();
        let timestamp = chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Nanos, true);
        println!(
            "{{\"time\":\"{}\",\"log_type\":\"system\",\"op\":\"logger.init\",\"level\":\"info\",\"message\":\"ACL Logger: JSON structured logging pipeline initialized. Level={:?}\"}}",
            timestamp, level
        );
    }

    /// Ghi nhận nhật ký gỡ lỗi hệ thống cấp thấp (System Debug Logs).
    pub fn sys_debug(op: &str, message: &str) {
        if Self::get_level() <= LogLevel::Debug {
            let timestamp = chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Nanos, true);
            let trace_seg = Self::trace_segment();
            println!(
                "{{\"time\":\"{}\",\"log_type\":\"{}\",\"op\":\"{}\",\"level\":\"debug\",\"message\":\"{}\"{}}}",
                timestamp, LOG_TYPE_SYSTEM, op, message, trace_seg
            );
        }
    }

    /// Ghi nhận nhật ký hệ thống cấp thông tin (System Info Logs).
    pub fn sys_info(op: &str, message: &str) {
        if Self::get_level() <= LogLevel::Info {
            let timestamp = chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Nanos, true);
            let trace_seg = Self::trace_segment();
            println!(
                "{{\"time\":\"{}\",\"log_type\":\"{}\",\"op\":\"{}\",\"level\":\"info\",\"message\":\"{}\"{}}}",
                timestamp, LOG_TYPE_SYSTEM, op, message, trace_seg
            );
        }
    }

    /// Ghi nhận nhật ký cảnh báo hệ thống (System Warn Logs).
    pub fn sys_warn(op: &str, message: &str, err_msg: &str) {
        if Self::get_level() <= LogLevel::Warn {
            let timestamp = chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Nanos, true);
            let trace_seg = Self::trace_segment();
            println!(
                "{{\"time\":\"{}\",\"log_type\":\"{}\",\"op\":\"{}\",\"level\":\"warn\",\"message\":\"{}\",\"error\":\"{}\"{}}}",
                timestamp, LOG_TYPE_SYSTEM, op, message, err_msg, trace_seg
            );
        }
    }

    /// Ghi nhận nhật ký lỗi hệ thống nghiêm trọng (System Error Logs).
    pub fn sys_error(op: &str, message: &str, err_msg: &str) {
        if Self::get_level() <= LogLevel::Error {
            let timestamp = chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Nanos, true);
            let trace_seg = Self::trace_segment();
            eprintln!(
                "{{\"time\":\"{}\",\"log_type\":\"{}\",\"op\":\"{}\",\"level\":\"error\",\"message\":\"{}\",\"error\":\"{}\"{}}}",
                timestamp, LOG_TYPE_SYSTEM, op, message, err_msg, trace_seg
            );
        }
    }

    /// Ghi nhận toàn bộ quyết định authorize từ ext_authz (Authz Decision Logs).
    pub fn authz_log(
        user_id: &str,
        method: &str,
        path: &str,
        decision: &str,
        reason: &str,
    ) {
        if Self::get_level() <= LogLevel::Info {
            let timestamp = chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Nanos, true);
            let trace_seg = Self::trace_segment();
            println!(
                "{{\"time\":\"{}\",\"log_type\":\"{}\",\"user_id\":\"{}\",\"method\":\"{}\",\"path\":\"{}\",\"decision\":\"{}\",\"reason\":\"{}\"{}}}",
                timestamp, LOG_TYPE_AUTHZ, user_id, method, path, decision, reason, trace_seg
            );
        }
    }
}
