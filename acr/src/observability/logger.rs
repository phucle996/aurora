use serde_json::{json, Map, Value};
use std::sync::OnceLock;
use uuid::Uuid;

// ============================================================================
// 📂 MODULE: observability/logger.rs - Hệ Thống Logs Cấu Trúc JSON Phân Cấp
// ============================================================================
//
// 📌 VAI TRÒ (ROLE):
//   - Xuất nhật ký hoạt động (logs) của ACL Service dưới dạng JSON cấu trúc (Structured Logging).
//   - Đồng bộ hóa 100% phong cách log với `pkg/logger.go` của Controlplane
//     và `observability/logger.rs` của Dataplane.
//   - Cung cấp hai phân hệ ghi logs: SystemLog (vận hành) và AuthzLog (quyết định authorize).
//   - Tuân thủ nghiêm ngặt cấp độ ghi log (`APP_LOG_LEVEL`) được cấu hình trong hệ thống.
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Nhật ký xuất ra trực tiếp luồng ra tiêu chuẩn lỗi `std::stderr` của container,
//     được thu thập bởi FluentBit / Promtail để đẩy lên Grafana Loki.
//
// 🔒 RANH GIỚI BẢO MẬT (PRIVACY BOUNDARY):
//   - TUYỆT ĐỐI CẤM ghi đè (log) toàn bộ nội dung JWT token, session secrets.
//   - Chỉ được ghi nhận các nhãn kỹ thuật thô phục vụ gỡ lỗi.
//
// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
//   - Mọi log ghi ra bắt buộc phải mang mốc thời gian độ phân giải cao `RFC3339Nano`
//     để phân tích chính xác thứ tự xảy ra sự kiện trên production.

// Nhãn phân loại log
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

struct LoggerIdentity {
    service_name: &'static str,
    service_version: String,
    service_instance_id: String,
}

static IDENTITY: OnceLock<LoggerIdentity> = OnceLock::new();

impl Logger {
    fn identity() -> &'static LoggerIdentity {
        IDENTITY.get_or_init(|| LoggerIdentity {
            service_name: "aurora-acr",
            service_version: std::env::var("APP_VERSION")
                .ok()
                .filter(|value| !value.trim().is_empty())
                .unwrap_or_else(|| env!("CARGO_PKG_VERSION").to_string()),
            // A process incarnation is stable for its lifetime and does not expose
            // the Docker/Kubernetes container ID as a stream dimension.
            service_instance_id: Uuid::new_v4().to_string(),
        })
    }

    pub(crate) fn service_version() -> &'static str {
        Self::identity().service_version.as_str()
    }

    /// Lấy cấu hình log level hiện tại của ứng dụng (sử dụng cache OnceLock cực nhanh).
    fn get_level() -> LogLevel {
        *CURRENT_LOG_LEVEL.get_or_init(|| {
            let level_str = std::env::var("APP_LOG_LEVEL").unwrap_or_else(|_| "info".to_string());
            LogLevel::from_str(&level_str)
        })
    }

    fn current_correlation() -> Map<String, Value> {
        let mut fields = Map::new();
        let (trace_id, span_id) = crate::observability::otel::OtelTracer::current_span_ids();
        if let Some(trace_id) = trace_id {
            fields.insert("trace_id".to_string(), Value::String(trace_id));
        }
        if let Some(span_id) = span_id {
            fields.insert("span_id".to_string(), Value::String(span_id));
        }
        fields
    }

    // [COMMENT]: Lấy mốc thời gian hiện tại theo múi giờ local của ứng dụng (được điều khiển qua biến môi trường TZ chuẩn).
    fn get_timestamp() -> String {
        chrono::Local::now().to_rfc3339_opts(chrono::SecondsFormat::Nanos, false)
    }

    fn emit(
        level: LogLevel,
        op: &str,
        event_code: &str,
        message: &str,
        error: Option<&str>,
        mut fields: Map<String, Value>,
    ) {
        let identity = Self::identity();
        fields.insert(
            "timestamp".to_string(),
            Value::String(Self::get_timestamp()),
        );
        fields.insert(
            "severity_text".to_string(),
            Value::String(
                match level {
                    LogLevel::Debug => "DEBUG",
                    LogLevel::Info => "INFO",
                    LogLevel::Warn => "WARN",
                    LogLevel::Error => "ERROR",
                }
                .to_string(),
            ),
        );
        fields.insert(
            "severity_number".to_string(),
            json!(match level {
                LogLevel::Debug => 5,
                LogLevel::Info => 9,
                LogLevel::Warn => 13,
                LogLevel::Error => 17,
            }),
        );
        fields.insert(
            "service_name".to_string(),
            Value::String(identity.service_name.to_string()),
        );
        fields.insert(
            "service_version".to_string(),
            Value::String(identity.service_version.clone()),
        );
        fields.insert(
            "service_instance_id".to_string(),
            Value::String(identity.service_instance_id.clone()),
        );
        fields.insert("op".to_string(), Value::String(op.to_string()));
        fields.insert(
            "event_code".to_string(),
            Value::String(event_code.to_string()),
        );
        fields.insert("message".to_string(), Value::String(message.to_string()));
        if let Some(error) = error.filter(|value| !value.trim().is_empty()) {
            fields.insert("error_cause".to_string(), Value::String(error.to_string()));
        }
        fields.extend(Self::current_correlation());
        let encoded = Value::Object(fields).to_string();
        match level {
            LogLevel::Error => eprintln!("{encoded}"),
            _ => println!("{encoded}"),
        }
    }

    /// Ghi nhận nhật ký gỡ lỗi hệ thống cấp thấp (System Debug Logs).
    pub fn sys_debug(op: &str, message: &str) {
        if Self::get_level() <= LogLevel::Debug {
            Self::emit(
                LogLevel::Debug,
                op,
                "SYSTEM_DEBUG",
                message,
                None,
                Map::new(),
            );
        }
    }

    /// Ghi nhận nhật ký hệ thống cấp thông tin (System Info Logs).
    pub fn sys_info(op: &str, message: &str) {
        if Self::get_level() <= LogLevel::Info {
            Self::emit(LogLevel::Info, op, "SYSTEM_INFO", message, None, Map::new());
        }
    }

    /// Ghi nhận nhật ký cảnh báo hệ thống (System Warn Logs).
    pub fn sys_warn(op: &str, message: &str, err_msg: &str) {
        if Self::get_level() <= LogLevel::Warn {
            Self::emit(
                LogLevel::Warn,
                op,
                "SYSTEM_WARNING",
                message,
                Some(err_msg),
                Map::new(),
            );
        }
    }

    /// Ghi nhận nhật ký lỗi hệ thống nghiêm trọng (System Error Logs).
    pub fn sys_error(op: &str, message: &str, err_msg: &str) {
        if Self::get_level() <= LogLevel::Error {
            Self::emit(
                LogLevel::Error,
                op,
                "SYSTEM_ERROR",
                message,
                Some(err_msg),
                Map::new(),
            );
        }
    }

    /// Ghi nhận toàn bộ quyết định authorize từ ext_authz (Authz Decision Logs).
    pub fn authz_log(user_id: &str, method: &str, path: &str, decision: &str, reason: &str) {
        if Self::get_level() <= LogLevel::Info {
            let mut fields = Map::new();
            fields.insert("actor_id".to_string(), Value::String(user_id.to_string()));
            fields.insert("method".to_string(), Value::String(method.to_string()));
            fields.insert("route".to_string(), Value::String(path.to_string()));
            fields.insert("decision".to_string(), Value::String(decision.to_string()));
            if !reason.trim().is_empty() {
                fields.insert("reason".to_string(), Value::String(reason.to_string()));
            }
            Self::emit(
                LogLevel::Info,
                "authz.ext_authz.check",
                "AUTHZ_DECISION",
                "Authorization decision",
                None,
                fields,
            );
        }
    }
}
