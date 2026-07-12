#![allow(dead_code)]
use std::collections::HashMap;
use std::sync::OnceLock;

/// ============================================================================
/// 📂 MODULE: logger.rs - Hệ Thống Logs Cấu Trúc JSON Phân Cấp cho Job Proxy
/// ============================================================================

pub const LOG_TYPE_ACCESS: &str = "access";
pub const LOG_TYPE_SYSTEM: &str = "system";
pub const LOG_TYPE_JOB: &str = "job";

pub struct Fields(pub HashMap<String, serde_json::Value>);

/// Phân cấp độ ưu tiên của Log Level
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
pub enum LogLevel {
    Info = 1,
    Warn = 2,
    Error = 3,
}

impl LogLevel {
    pub fn from_str(s: &str) -> Self {
        match s.to_lowercase().as_str() {
            "warn" => LogLevel::Warn,
            "error" => LogLevel::Error,
            _ => LogLevel::Info, // Mặc định là Info
        }
    }
}

static CURRENT_LOG_LEVEL: OnceLock<LogLevel> = OnceLock::new();

pub struct Logger;

impl Logger {
    /// Lấy cấu hình log level hiện tại của ứng dụng
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

    /// Lấy phân đoạn trace_id định dạng JSON để gắn vào logs
    fn get_trace_segment() -> String {
        if let Some(tid) = crate::observability::otel::OtelTracer::get_current_trace_id() {
            format!(",\"trace_id\":\"{}\"", tid)
        } else {
            "".to_string()
        }
    }

    /// Ghi nhận nhật ký truy cập mạng/kết nối mạng (Access Logs).
    pub fn access_log(op: &str, method: &str, route: &str, status_code: i32, latency_ms: f64) {
        if Self::get_level() <= LogLevel::Info {
            let timestamp = chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Nanos, true);
            let trace_segment = Self::get_trace_segment();
            println!(
                "{{\"time\":\"{}\",\"log_type\":\"{}\",\"op\":\"{}\",\"method\":\"{}\",\"route\":\"{}\",\"status_code\":{},\"latency_ms\":{:.3}{}}}",
                timestamp, LOG_TYPE_ACCESS, op, method, route, status_code, latency_ms, trace_segment
            );
        }
    }

    /// Ghi nhận nhật ký hệ thống cấp thấp (System Info Logs).
    pub fn sys_info(op: &str, message: &str) {
        if Self::get_level() <= LogLevel::Info {
            let timestamp = chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Nanos, true);
            let trace_segment = Self::get_trace_segment();
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
            let trace_segment = Self::get_trace_segment();
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
            let trace_segment = Self::get_trace_segment();
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
            let trace_segment = Self::get_trace_segment();
            println!(
                "{{\"time\":\"{}\",\"log_type\":\"{}\",\"level\":\"info\",\"job_id\":\"{}\",\"job_topic\":\"{}\",\"attempt\":{},\"op\":\"{}\",\"message\":\"{}\"{}}}",
                timestamp, LOG_TYPE_JOB, job_id, job_topic, attempt, op, message, trace_segment
            );
        }
    }
}
