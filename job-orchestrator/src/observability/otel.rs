use crate::config::{Config, get_node_hostname};
use crate::observability::logger::Logger;
use opentelemetry::trace::{SpanContext, SpanId, TraceFlags, TraceId};
use opentelemetry::{global, KeyValue};
use opentelemetry_otlp::WithExportConfig;
use opentelemetry_sdk::{
    propagation::TraceContextPropagator,
    trace::{self, Sampler},
    metrics::{PeriodicReader, SdkMeterProvider},
    Resource,
};
use tokio::task_local;

// ============================================================================
// 📂 MODULE: observability/otel.rs - OpenTelemetry Integrations for Job Proxy
// ============================================================================

task_local! {
    /// Lưu trữ trace_id thô của async task hiện tại nhằm liên kết logs và traces.
    pub static CURRENT_TRACE_ID: String;
}

// Lưu giữ Meter Provider toàn cục để giải phóng tài nguyên khi đóng app (HA design)
static METER_PROVIDER: std::sync::OnceLock<SdkMeterProvider> = std::sync::OnceLock::new();

/// Trình quản lý hệ thống theo vết (Tracer) và đo lường (Metrics) chuẩn OpenTelemetry
pub struct OtelTracer;

impl OtelTracer {
    /// Khởi tạo OpenTelemetry (bao gồm cả Traces và Metrics) cho Job Proxy
    pub fn init(config: &Config) {
        Self::init_tracer(config);
        Self::init_metrics(config);
    }

    /// Khởi tạo OpenTelemetry tracer pipeline thực tế kết nối tới Tempo/OTel Collector
    fn init_tracer(config: &Config) {
        // Thiết lập bộ truyền Trace Context chuẩn W3C (traceparent)
        global::set_text_map_propagator(TraceContextPropagator::new());

        let endpoint = &config.otel_exporter_otlp_endpoint;
        let zone_id = &config.zone_id;
        let hostname = get_node_hostname();

        // Định danh tài nguyên nghiệp vụ trong hệ thống giám sát tập trung
        let resource = Resource::new(vec![
            KeyValue::new("service.name", "aurora-job-orchestrator"),
            KeyValue::new("zone_id", zone_id.clone()),
            KeyValue::new("hostname", hostname),
        ]);

        // Tạo exporter gRPC Tonic kết nối trực tiếp đến OTel Collector
        let otlp_exporter = opentelemetry_otlp::new_exporter()
            .tonic()
            .with_endpoint(endpoint);

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
                        "Observability OTel: Real OpenTelemetry tracer pipeline initialized. Exporting to OTLP collector at {}",
                        endpoint
                     ),
                );
            }
            Err(e) => {
                Logger::sys_error(
                    "tracing.init",
                    &format!("Failed to initialize OTel tracing pipeline: {:?}", e),
                    "otel_init_error",
                );
            }
        }
    }

    /// Khởi tạo OpenTelemetry metrics pipeline đẩy dữ liệu lên OTel Collector định kỳ
    fn init_metrics(config: &Config) {
        let endpoint = &config.otel_exporter_otlp_endpoint;
        let zone_id = &config.zone_id;
        let hostname = get_node_hostname();

        // Thiết lập resource attributes mô tả nguồn gốc metrics
        let resource = Resource::new(vec![
            KeyValue::new("service.name", "aurora-job-orchestrator"),
            KeyValue::new("zone_id", zone_id.clone()),
            KeyValue::new("hostname", hostname),
        ]);

        // Xây dựng Metric Exporter theo chuẩn OTLP gRPC Tonic
        let otlp_exporter = match opentelemetry_otlp::new_exporter()
            .tonic()
            .with_endpoint(endpoint)
            .build_metrics_exporter(
                Box::new(opentelemetry_sdk::metrics::reader::DefaultAggregationSelector::new()),
                Box::new(opentelemetry_sdk::metrics::reader::DefaultTemporalitySelector::new()),
            )
        {
            Ok(exp) => exp,
            Err(e) => {
                Logger::sys_error(
                    "metrics.init",
                    &format!("Failed to build OTel metrics exporter: {:?}", e),
                    "metrics_exporter_error",
                );
                return;
            }
        };

        // Thiết lập bộ đọc định kỳ đẩy metrics về OTel Collector mỗi 15 giây (HA push model)
        let reader = PeriodicReader::builder(otlp_exporter, opentelemetry_sdk::runtime::Tokio)
            .with_interval(std::time::Duration::from_secs(15))
            .build();

        let provider = SdkMeterProvider::builder()
            .with_reader(reader)
            .with_resource(resource)
            .build();

        global::set_meter_provider(provider.clone());
        let _ = METER_PROVIDER.set(provider);

        Logger::sys_info(
            "metrics.init",
            &format!(
                "Observability OTel: Real OpenTelemetry metrics pipeline initialized. Pushing to OTLP collector at {}",
                endpoint
            ),
        );
    }

    /// Giải phóng tài nguyên và Flush toàn bộ traces/metrics còn lại trước khi container dừng
    #[allow(dead_code)]
    pub fn stop() {
        global::shutdown_tracer_provider();
        if let Some(provider) = METER_PROVIDER.get() {
            if let Err(e) = provider.shutdown() {
                Logger::sys_error(
                    "metrics.stop",
                    &format!("Failed to shutdown metrics provider: {:?}", e),
                    "metrics_shutdown_error",
                );
            }
        }
        Logger::sys_info(
            "observability.stop",
            "Observability OTel: Tracer and Meter providers shutdown and all data flushed.",
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
