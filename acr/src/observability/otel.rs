use opentelemetry::{global, trace::TraceContextExt, Context, KeyValue};
use opentelemetry_otlp::WithExportConfig;
use opentelemetry_sdk::{
    metrics::{PeriodicReader, SdkMeterProvider},
    propagation::TraceContextPropagator,
    trace::{self, Sampler},
    Resource,
};

// ============================================================================
// 📂 MODULE: observability/otel.rs - Liên Kết Vết Xử Lý Hệ Thống (OpenTelemetry)
// ============================================================================
//
// 📌 VAI TRÒ (ROLE):
//   - Thiết lập kết nối OTLP gRPC thực tế đến Tempo (Tracing) và VictoriaMetrics (Metrics).
//   - Đọc trace context chuẩn OTel để đồng bộ hóa log và trace.
//   - Cung cấp cơ chế đóng gói ngữ cảnh xử lý bất đồng bộ (HA context propagation).
//   - Đồng bộ 100% pattern với observability/otel.rs của Dataplane.

// Cache Meter Provider toàn cục phục vụ graceful shutdown
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

        // Resource attributes: metadata nhận dạng nguồn gốc trace trong Tempo/Grafana
        let hostname = crate::config::get_node_hostname();
        let resource = Resource::new(vec![
            KeyValue::new("hostname", hostname),
            KeyValue::new("service.name", "aurora-acr"),
            KeyValue::new(
                "service.version",
                crate::observability::logger::Logger::service_version().to_string(),
            ),
        ]);

        // Xây dựng gRPC Tonic exporter kết nối tới Collector
        let otlp_exporter = opentelemetry_otlp::new_exporter()
            .tonic()
            .with_endpoint(endpoint);

        // Đăng ký pipeline bất đồng bộ dạng Batch để không cản trở luồng xử lý chính
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
                        "ACL OTel: Tracer pipeline initialized. Exporting to OTLP collector at {}",
                        endpoint
                    ),
                );
            }
            Err(e) => {
                crate::observability::logger::Logger::sys_error(
                    "tracing.init",
                    &format!("Failed to initialize OTel tracer pipeline: {:?}", e),
                    "otel_tracer_init_error",
                );
            }
        }
    }

    /// Khởi tạo OpenTelemetry metrics pipeline đẩy dữ liệu lên OTel Collector.
    fn init_metrics(config: &crate::config::Config) {
        let endpoint = &config.otel_exporter_otlp_endpoint;

        let hostname = crate::config::get_node_hostname();
        let resource = Resource::new(vec![
            KeyValue::new("hostname", hostname),
            KeyValue::new("service.name", "aurora-acr"),
            KeyValue::new(
                "service.version",
                crate::observability::logger::Logger::service_version().to_string(),
            ),
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
                "ACL OTel: Metrics pipeline initialized. Pushing to OTLP collector at {}",
                endpoint
            ),
        );
    }

    /// Read the active OTel context. This keeps log correlation aligned with
    /// the span propagated through HTTP/Redis boundaries instead of a second
    /// task-local trace carrier that can silently diverge.
    pub fn current_span_ids() -> (Option<String>, Option<String>) {
        let context = Context::current();
        let span = context.span();
        let span_context = span.span_context();
        if !span_context.is_valid() {
            return (None, None);
        }
        (
            Some(span_context.trace_id().to_string()),
            Some(span_context.span_id().to_string()),
        )
    }
}
