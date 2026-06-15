use crate::config::Config;
use crate::observability::logger::Logger;
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
// 📂 MODULE: observability/otel.rs - OpenTelemetry Integrations for Notification Service
// ============================================================================

// Cấu trúc chứa thông tin Trace Context theo tiêu chuẩn W3C Trace Context
#[derive(Clone, Debug)]
pub struct TraceContext {
    pub trace_id: String, // 32 ký tự hex đại diện cho Trace ID
    pub span_id: String,  // 16 ký tự hex đại diện cho Span ID
}

impl TraceContext {
    /// Phân tích cú pháp chuỗi traceparent W3C (ví dụ: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01)
    pub fn parse(traceparent: &str) -> Option<Self> {
        let parts: Vec<&str> = traceparent.split('-').collect();
        if parts.len() >= 4 && parts[0] == "00" {
            Some(Self {
                trace_id: parts[1].to_string(),
                span_id: parts[2].to_string(),
            })
        } else {
            None
        }
    }

    /// Khởi tạo một Trace Context mới ngẫu nhiên sử dụng uuid
    pub fn new_random() -> Self {
        // Simple uuid v4 tạo ra chuỗi 32 ký tự hex ngẫu nhiên không có dấu gạch ngang
        let trace_id = uuid::Uuid::new_v4().simple().to_string();
        // Lấy 16 ký tự đầu của một uuid v4 khác làm span_id ngẫu nhiên
        let span_id = uuid::Uuid::new_v4().simple().to_string()[..16].to_string();

        Self { trace_id, span_id }
    }

    /// Tạo chuỗi định dạng traceparent chuẩn W3C để truyền đi qua HTTP/gRPC headers
    pub fn to_traceparent(&self) -> String {
        format!("00-{}-{}-01", self.trace_id, self.span_id)
    }
}

// Định nghĩa biến tĩnh lưu trữ TraceContext cục bộ cho mỗi Async Task của Tokio (Task-Local Storage)
task_local! {
    pub static CURRENT_TRACE: TraceContext;
}

// Lưu giữ Meter Provider toàn cục để giải phóng tài nguyên khi đóng app (HA design)
static METER_PROVIDER: std::sync::OnceLock<SdkMeterProvider> = std::sync::OnceLock::new();

pub struct OtelTracer;

impl OtelTracer {
    /// Khởi tạo OpenTelemetry (bao gồm cả Traces và Metrics) cho Notification Service
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
            KeyValue::new("service.name", "aurora-notification-service"),
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
            KeyValue::new("service.name", "aurora-notification-service"),
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

    /// Lấy TraceContext hiện tại của async task nếu có
    pub fn get_current_trace() -> Option<TraceContext> {
        CURRENT_TRACE.try_with(|t| t.clone()).ok()
    }

    /// Lấy chuỗi traceparent hiện tại của async task nếu có
    pub fn get_traceparent() -> Option<String> {
        CURRENT_TRACE.try_with(|t| t.to_traceparent()).ok()
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
}

/// Hàm phụ trợ lấy Hostname của node hiện tại phục vụ định danh tài nguyên (Resource Attributes)
pub fn get_node_hostname() -> String {
    std::env::var("HOSTNAME")
        .unwrap_or_else(|_| {
            hostname::get()
                .map(|h| h.into_string().unwrap_or_default())
                .unwrap_or_default()
        })
}
