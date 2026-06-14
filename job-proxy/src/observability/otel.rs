use crate::observability::logger::Logger;
use opentelemetry::trace::{SpanContext, SpanId, TraceFlags, TraceId};
use opentelemetry::{global, KeyValue};
use opentelemetry_otlp::WithExportConfig;
use opentelemetry_sdk::{
    propagation::TraceContextPropagator,
    trace::{self, Sampler},
    Resource,
};
use tokio::task_local;

task_local! {
    /// Lưu trữ trace_id thô của async task hiện tại nhằm liên kết logs và traces.
    pub static CURRENT_TRACE_ID: String;
}

/// Hệ thống theo vết xử lý OpenTelemetry tích hợp kết nối Tempo thực tế
pub struct OtelTracer;

impl OtelTracer {
    /// Khởi tạo OpenTelemetry tracer pipeline thực tế kết nối tới Tempo Collector
    pub fn init() {
        // Thiết lập bộ truyền Trace Context chuẩn W3C (traceparent)
        global::set_text_map_propagator(TraceContextPropagator::new());

        // Lấy endpoint OTLP gRPC từ biến môi trường (mặc định trỏ tới otel-collector:4317)
        let endpoint = std::env::var("OTEL_EXPORTER_OTLP_ENDPOINT")
            .unwrap_or_else(|_| "http://otel-collector:4317".to_string());

        // Metadata nhận dạng tài nguyên trong Grafana/Tempo
        let zone_id = std::env::var("ZONE_ID").unwrap_or_else(|_| "unknown".to_string());
        let hostname = std::env::var("HOSTNAME")
            .unwrap_or_else(|_| hostname::get().map(|h| h.into_string().unwrap_or_default()).unwrap_or_default());
        
        let resource = Resource::new(vec![
            KeyValue::new("service.name", "aurora-job-proxy"),
            KeyValue::new("zone_id", zone_id),
            KeyValue::new("hostname", hostname),
        ]);

        // Tạo exporter gRPC Tonic kết nối trực tiếp đến Collector
        let otlp_exporter = opentelemetry_otlp::new_exporter()
            .tonic()
            .with_endpoint(&endpoint);

        // Khởi tạo batch pipeline để tránh tắc nghẽn luồng xử lý chính (HA production design)
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
                Logger::sys_info(
                    "tracing.init",
                    &format!(
                        "Observability OTel: Real OpenTelemetry tracer pipeline initialized. Exporting to Tempo at {}",
                        endpoint
                    ),
                );
            }
            Err(e) => {
                Logger::sys_error(
                    "tracing.init",
                    &format!("Failed to initialize OTel pipeline: {:?}", e),
                    "otel_init_error",
                );
            }
        }
    }

    /// Giải phóng tài nguyên và Flush toàn bộ trace spans còn lại trước khi container tắt
    #[allow(dead_code)]
    pub fn stop() {
        global::shutdown_tracer_provider();
        Logger::sys_info(
            "tracing.stop",
            "Observability OTel: Tracer provider shutdown and all spans flushed.",
        );
    }

    /// Lấy trace ID của async task hiện tại phục vụ cơ chế tự động đính kèm vào Logs
    pub fn get_current_trace_id() -> Option<String> {
        CURRENT_TRACE_ID.try_with(|tid| tid.clone()).ok()
    }

    /// Phân tích cú pháp trace_id hoặc traceparent chuẩn W3C thành SpanContext
    pub fn parse_traceparent(traceparent: &str) -> Option<SpanContext> {
        let parts: Vec<&str> = traceparent.split('-').collect();
        if parts.len() >= 4 && parts[0] == "00" {
            // Định dạng chuẩn W3C: 00-traceid-spanid-flags
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
            // Chuỗi trace_id thô 32 ký tự hex từ postgres outbox
            let trace_id = TraceId::from_hex(traceparent).ok()?;
            let span_id = SpanId::from_hex("00f067aa0ba902b7").ok()?; // Span ID mặc định
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
