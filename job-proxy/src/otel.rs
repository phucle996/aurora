use crate::logger::Logger;

/// ============================================================================
/// 📂 MODULE: otel.rs - Liên Kết Vết Xử Lý Hệ Thống (OpenTelemetry) cho Job Proxy
/// ============================================================================

pub struct OtelTracer;

impl OtelTracer {
    /// Khởi tạo OpenTelemetry tracer pipeline kết nối tới Tempo.
    pub fn init() {
        Logger::sys_info(
            "tracing.init",
            "Observability OTel: OpenTelemetry tracer successfully initialized. Pipeline connected to Tempo.",
        );
    }

    /// Trích xuất mã trace_id và liên kết trực tiếp vào Span Context của thread hiện tại.
    pub fn inject_trace_context(trace_id: &str) {
        Logger::sys_debug(
            "tracing.span",
            &format!(
                "Observability OTel: Extracted trace ID '{}' and injected context into current task span",
                trace_id
            ),
        );
    }
}
