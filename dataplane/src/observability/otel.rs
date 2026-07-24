use opentelemetry::trace::{SpanContext, SpanId, TraceFlags, TraceId};
use opentelemetry::{global, KeyValue};
use opentelemetry_otlp::WithExportConfig;
use opentelemetry_sdk::{
    metrics::{PeriodicReader, SdkMeterProvider},
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
//   - Thiết lập kết nối OTLP gRPC thực tế đến hệ thống thu thập Metrics (VictoriaMetrics qua OTel).
//   - Quản lý trace ID bằng Task-Local Storage của Tokio để đồng bộ hóa Loki log và Tempo trace.
//   - Cung cấp cơ chế đóng gói ngữ cảnh xử lý bất đồng bộ (HA context propagation).

task_local! {
    /// Lưu trữ trace_id thô của async task hiện tại nhằm liên kết logs và traces.
    pub static CURRENT_TRACE_ID: String;
}

static METER_PROVIDER: std::sync::OnceLock<SdkMeterProvider> = std::sync::OnceLock::new();

pub struct OtelTracer;

impl OtelTracer {
    /// Khởi tạo OpenTelemetry (cả Tracing và Metrics) kết nối tới Collector.
    pub fn init(config: &crate::config::Config) {
        Self::init_tracer(config);
        Self::init_metrics(config);
    }

    /// Khởi tạo OpenTelemetry tracer pipeline thực tế kết nối tới Tempo Collector.
    fn init_tracer(config: &crate::config::Config) {
        // Thiết lập bộ truyền Trace Context chuẩn W3C (traceparent)
        global::set_text_map_propagator(TraceContextPropagator::new());

        let endpoint = &config.otel_exporter_otlp_endpoint;
        let zone_id = &config.zone_id;

        // Resource attributes: metadata nhận dạng nguồn gốc trace trong Tempo/Grafana
        let hostname = crate::config::get_node_hostname();
        let resource = Resource::new(vec![
            KeyValue::new("zone_id", zone_id.clone()),
            KeyValue::new("hostname", hostname),
            KeyValue::new("service.name", "aurora-dataplane"),
        ]);

        // Xây dựng gRPC Tonic exporter kết nối tới Collector
        let otlp_exporter = opentelemetry_otlp::new_exporter()
            .tonic()
            .with_endpoint(endpoint);

        // Đăng ký pipeline bất đồng bộ dạng Batch để không cản trở luồng xử lý chính của worker (High Availability)
        match opentelemetry_otlp::new_pipeline()
            .tracing()
            .with_exporter(otlp_exporter)
            .with_trace_config(
                trace::Config::default()
                    // [COMMENT]: ParentBased preserves upstream trace continuity; only new root
                    // traces are ratio-sampled to cap CPU/network at high job throughput.
                    .with_sampler(Sampler::ParentBased(Box::new(Sampler::TraceIdRatioBased(
                        config.otel_trace_sample_ratio,
                    ))))
                    .with_resource(resource),
            )
            .install_batch(opentelemetry_sdk::runtime::Tokio)
        {
            Ok(_) => {
                crate::observability::logger::Logger::sys_info(
                    "tracing.init",
                    &format!(
                        "Observability OTel tracer initialized; endpoint={}, root_sample_ratio={}",
                        endpoint, config.otel_trace_sample_ratio
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

    /// Khởi tạo OpenTelemetry metrics pipeline đẩy dữ liệu lên OTel Collector.
    fn init_metrics(config: &crate::config::Config) {
        let endpoint = &config.otel_exporter_otlp_endpoint;
        let zone_id = &config.zone_id;

        let hostname = crate::config::get_node_hostname();
        let resource = Resource::new(vec![
            KeyValue::new("zone_id", zone_id.clone()),
            KeyValue::new("hostname", hostname),
            KeyValue::new("service.name", "aurora-dataplane"),
        ]);

        // Khởi tạo OTLP Metric Exporter builder theo chuẩn tonic
        let otlp_exporter = match opentelemetry_otlp::new_exporter()
            .tonic()
            .with_endpoint(endpoint)
            .build_metrics_exporter(
                Box::new(opentelemetry_sdk::metrics::reader::DefaultAggregationSelector::new()),
                Box::new(opentelemetry_sdk::metrics::reader::DefaultTemporalitySelector::new()),
            ) {
            Ok(exp) => exp,
            Err(e) => {
                crate::observability::logger::Logger::sys_error(
                    "metrics.init",
                    &format!("Failed to build OTel metrics exporter: {:?}", e),
                    "metrics_exporter_error",
                );
                return;
            }
        };

        // Thiết lập bộ đọc định kỳ đẩy metrics về OTel Collector mỗi 15 giây
        let reader = PeriodicReader::builder(otlp_exporter, opentelemetry_sdk::runtime::Tokio)
            .with_interval(std::time::Duration::from_secs(15))
            .build();

        let provider = SdkMeterProvider::builder()
            .with_reader(reader)
            .with_resource(resource)
            .build();

        global::set_meter_provider(provider.clone());
        let _ = METER_PROVIDER.set(provider);

        crate::observability::logger::Logger::sys_info(
            "metrics.init",
            &format!(
                "Observability OTel: Real OpenTelemetry metrics pipeline initialized. Pushing to OTLP collector at {}",
                endpoint
            ),
        );
    }

    /// Giải phóng tài nguyên và Flush toàn bộ traces/metrics còn lại trong buffer trước khi dừng container.
    pub fn stop() {
        global::shutdown_tracer_provider();
        if let Some(provider) = METER_PROVIDER.get() {
            if let Err(e) = provider.shutdown() {
                crate::observability::logger::Logger::sys_error(
                    "metrics.stop",
                    &format!("Failed to shutdown metrics provider: {:?}", e),
                    "metrics_shutdown_error",
                );
            }
        }
        crate::observability::logger::Logger::sys_info(
            "observability.stop",
            "Observability OTel: Tracer and Meter providers shutdown and all data flushed.",
        );
    }

    /// Lấy trace ID hiện tại của async task phục vụ việc chèn vào logs.
    pub fn get_current_trace_id() -> Option<String> {
        CURRENT_TRACE_ID
            .try_with(|value| {
                let value = value.trim();
                if value.len() == 32 && value.bytes().all(|byte| byte.is_ascii_hexdigit()) {
                    return Some(value.to_ascii_lowercase());
                }
                let mut parts = value.split('-');
                match (
                    parts.next(),
                    parts.next(),
                    parts.next(),
                    parts.next(),
                    parts.next(),
                ) {
                    (Some("00"), Some(trace_id), Some(_span_id), Some(_flags), None)
                        if trace_id.len() == 32
                            && trace_id.bytes().all(|byte| byte.is_ascii_hexdigit()) =>
                    {
                        Some(trace_id.to_ascii_lowercase())
                    }
                    _ => None,
                }
            })
            .ok()
            .flatten()
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
