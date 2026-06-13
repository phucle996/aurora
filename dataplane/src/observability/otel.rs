use opentelemetry::trace::{SpanContext, SpanId, TraceFlags, TraceId};
use opentelemetry::{global, KeyValue};
use opentelemetry_otlp::WithExportConfig;
use opentelemetry_sdk::{
    propagation::TraceContextPropagator,
    trace::{self, Sampler},
    Resource,
};
use tokio::task_local;

// ============================================================================
// 📂 MODULE: observability/otel.rs - Liên Kết Vết Xử Lý Hệ Thống (OpenTelemetry)
// ============================================================================
//
// 📌 VAI TRÒ (ROLE):
//   - Thiết lập kết nối OTLP gRPC thực tế đến hệ thống thu thập Tracing tập trung (Tempo).
//   - Quản lý trace ID bằng Task-Local Storage của Tokio để đồng bộ hóa Loki log và Tempo trace.
//   - Cung cấp cơ chế đóng gói ngữ cảnh xử lý bất đồng bộ (HA context propagation).

task_local! {
    /// Lưu trữ trace_id thô của async task hiện tại nhằm liên kết logs và traces.
    pub static CURRENT_TRACE_ID: String;
}

pub struct OtelTracer;

impl OtelTracer {
    /// Khởi tạo OpenTelemetry tracer pipeline thực tế kết nối tới Tempo Collector.
    pub fn init() {
        // Thiết lập bộ truyền Trace Context chuẩn W3C (traceparent)
        global::set_text_map_propagator(TraceContextPropagator::new());

        // Lấy endpoint từ cấu hình biến môi trường của môi trường Cloud Native
        let endpoint = std::env::var("OTEL_EXPORTER_OTLP_ENDPOINT")
            .unwrap_or_else(|_| "http://otel-collector:4317".to_string());

        // Resource attributes: metadata nhận dạng nguồn gốc trace trong Tempo/Grafana
        let zone_id = std::env::var("ZONE_ID").unwrap_or_else(|_| "unknown".to_string());
        let hostname = crate::config::get_node_hostname();
        let resource = Resource::new(vec![
            KeyValue::new("zone_id", zone_id),
            KeyValue::new("hostname", hostname),
        ]);

        // Xây dựng gRPC Tonic exporter kết nối tới Collector
        let otlp_exporter = opentelemetry_otlp::new_exporter()
            .tonic()
            .with_endpoint(&endpoint);

        // Đăng ký pipeline bất đồng bộ dạng Batch để không cản trở luồng xử lý chính của worker (High Availability)
        match opentelemetry_otlp::new_pipeline()
            .tracing()
            .with_exporter(otlp_exporter)
            .with_trace_config(
                trace::Config::default()
                    .with_sampler(Sampler::AlwaysOn)
                    .with_resource(resource),
            )
            .install_batch(opentelemetry_sdk::runtime::Tokio)
        {
            Ok(_) => {
                crate::observability::logger::Logger::sys_info(
                    "tracing.init",
                    &format!(
                        "Observability OTel: Real OpenTelemetry tracer pipeline initialized. Exporting to OTLP collector at {}",
                        endpoint
                    ),
                );
            }
            Err(e) => {
                crate::observability::logger::Logger::sys_error(
                    "tracing.init",
                    &format!("Failed to initialize OTel pipeline: {:?}", e),
                    "otel_init_error",
                );
            }
        }
    }

    /// Giải phóng tài nguyên và Flush toàn bộ traces còn lại trong buffer trước khi dừng container.
    pub fn stop() {
        global::shutdown_tracer_provider();
        crate::observability::logger::Logger::sys_info(
            "tracing.stop",
            "Observability OTel: Tracer provider shutdown and all spans flushed.",
        );
    }

    /// Lấy trace ID hiện tại của async task phục vụ việc chèn vào logs.
    pub fn get_current_trace_id() -> Option<String> {
        CURRENT_TRACE_ID.try_with(|tid| tid.clone()).ok()
    }

    /// Phân tích cú pháp chuỗi trace_id thô hoặc traceparent chuẩn W3C thành SpanContext.
    pub fn parse_traceparent(traceparent: &str) -> Option<SpanContext> {
        let parts: Vec<&str> = traceparent.split('-').collect();
        if parts.len() >= 4 && parts[0] == "00" {
            // Chuẩn W3C: 00-traceid-spanid-flags
            let trace_id = TraceId::from_hex(parts[1]).ok()?;
            let span_id = SpanId::from_hex(parts[2]).ok()?;
            Some(SpanContext::new(
                trace_id,
                span_id,
                TraceFlags::SAMPLED,
                true,
                opentelemetry::trace::TraceState::default(),
            ))
        } else if traceparent.len() == 32 {
            // Chuỗi trace_id 32 ký tự hex thô từ Controlplane cũ
            let trace_id = TraceId::from_hex(traceparent).ok()?;
            let span_id = SpanId::from_hex("00f067aa0ba902b7").ok()?; // Span ID mặc định giả lập
            Some(SpanContext::new(
                trace_id,
                span_id,
                TraceFlags::SAMPLED,
                true,
                opentelemetry::trace::TraceState::default(),
            ))
        } else {
            None
        }
    }
}
