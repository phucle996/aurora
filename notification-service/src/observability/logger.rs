use std::sync::OnceLock;

/// ============================================================================
/// 📂 MODULE: observability/logger.rs - Hệ Thống Logs Cấu Trúc JSON Phân Cấp
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Xuất nhật ký hoạt động (logs) của Notification Service dưới dạng JSON cấu trúc.
///   - Đồng bộ hóa 100% phong cách log với `pkg/logger.go` của Controlplane và Dataplane.
///
pub const LOG_TYPE_ACCESS: &str = "access";
pub const LOG_TYPE_SYSTEM: &str = "system";

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
            _ => LogLevel::Info,
        }
    }
}

static CURRENT_LOG_LEVEL: OnceLock<LogLevel> = OnceLock::new();

pub struct Logger;

impl Logger {
    fn get_level() -> LogLevel {
        *CURRENT_LOG_LEVEL.get_or_init(|| {
            let level_str = std::env::var("APP_LOG_LEVEL").unwrap_or_else(|_| "info".to_string());
            LogLevel::from_str(&level_str)
        })
    }

    // Active OTel context is authoritative for Redis/Kafka work; task-local is
    // retained only for the legacy connect handler until that path is migrated.
    fn get_trace_part() -> String {
        if let (Some(trace_id), Some(span_id)) = (
            crate::observability::otel::OtelTracer::get_current_trace_id(),
            crate::observability::otel::OtelTracer::get_current_span_id(),
        ) {
            format!(",\"trace_id\":\"{trace_id}\",\"span_id\":\"{span_id}\"")
        } else if let Some(ctx) = crate::observability::otel::OtelTracer::get_current_trace() {
            format!(",\"trace_id\":\"{}\"", ctx.trace_id)
        } else {
            "".to_string()
        }
    }

    // [COMMENT]: Lấy mốc thời gian hiện tại theo múi giờ local của ứng dụng (được điều khiển qua biến môi trường TZ chuẩn).
    fn get_timestamp() -> String {
        chrono::Local::now().to_rfc3339_opts(chrono::SecondsFormat::Nanos, false)
    }

    pub fn init() {
        let level = Self::get_level();
        let timestamp = Self::get_timestamp();
        let tz = std::env::var("TZ").unwrap_or_else(|_| "UTC".to_string());
        println!(
            "{{\"time\":\"{}\",\"log_type\":\"system\",\"op\":\"logger.init\",\"level\":\"info\",\"message\":\"Observability Logger: JSON structured logging pipeline initialized. Level={:?}, TZ={}\"}}",
            timestamp, level, tz
        );
    }

    pub fn access_log(op: &str, method: &str, route: &str, status_code: i32, latency_ms: f64) {
        if Self::get_level() <= LogLevel::Info {
            let timestamp = Self::get_timestamp();
            let trace_part = Self::get_trace_part();
            println!(
                "{{\"time\":\"{}\",\"log_type\":\"{}\",\"op\":\"{}\",\"method\":\"{}\",\"route\":\"{}\",\"status_code\":{},\"latency_ms\":{:.3}{}}}",
                timestamp, LOG_TYPE_ACCESS, op, method, route, status_code, latency_ms, trace_part
            );
        }
    }

    pub fn sys_info(op: &str, message: &str) {
        if Self::get_level() <= LogLevel::Info {
            let timestamp = Self::get_timestamp();
            let trace_part = Self::get_trace_part();
            println!(
                "{{\"time\":\"{}\",\"log_type\":\"{}\",\"op\":\"{}\",\"level\":\"info\",\"message\":\"{}\"{}}}",
                timestamp, LOG_TYPE_SYSTEM, op, message, trace_part
            );
        }
    }

    pub fn sys_warn(op: &str, message: &str, err_msg: &str) {
        if Self::get_level() <= LogLevel::Warn {
            let timestamp = Self::get_timestamp();
            let trace_part = Self::get_trace_part();
            println!(
                "{{\"time\":\"{}\",\"log_type\":\"{}\",\"op\":\"{}\",\"level\":\"warn\",\"message\":\"{}\",\"error\":\"{}\"{}}}",
                timestamp, LOG_TYPE_SYSTEM, op, message, err_msg, trace_part
            );
        }
    }

    pub fn sys_error(op: &str, message: &str, err_msg: &str) {
        if Self::get_level() <= LogLevel::Error {
            let timestamp = Self::get_timestamp();
            let trace_part = Self::get_trace_part();
            eprintln!(
                "{{\"time\":\"{}\",\"log_type\":\"{}\",\"op\":\"{}\",\"level\":\"error\",\"message\":\"{}\",\"error\":\"{}\"{}}}",
                timestamp, LOG_TYPE_SYSTEM, op, message, err_msg, trace_part
            );
        }
    }
}
