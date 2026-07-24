use crate::config::Config;
use crate::observability::logger::Logger;
use opentelemetry::trace::{SpanKind, Status, TraceContextExt, Tracer};
use opentelemetry::{global, Context, KeyValue};
use opentelemetry_otlp::WithExportConfig;
use opentelemetry_sdk::{
    metrics::{PeriodicReader, SdkMeterProvider},
    propagation::TraceContextPropagator,
    trace::{self, Sampler},
    Resource,
};
use std::borrow::Cow;
use std::collections::HashMap;
use tokio::task_local;

const MAX_TRACEPARENT_BYTES: usize = 128;
const MAX_TRACESTATE_BYTES: usize = 512;

pub struct PropagationContext {
    pub traceparent: String,
    pub tracestate: String,
}

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
    /// Phân tích cú pháp chuỗi traceparent W3C hoặc chuỗi trace_id thô 32 ký tự hex
    pub fn parse(traceparent: &str) -> Option<Self> {
        let parts: Vec<&str> = traceparent.split('-').collect();
        if parts.len() >= 4 && parts[0] == "00" {
            Some(Self {
                trace_id: parts[1].to_string(),
                span_id: parts[2].to_string(),
            })
        } else if traceparent.len() == 32 {
            // Trường hợp nhận chuỗi trace_id thô 32 ký tự hex từ Outbox DB hoặc các thành phần CDC
            Some(Self {
                trace_id: traceparent.to_string(),
                span_id: "00f067aa0ba902b7".to_string(), // Sử dụng Span ID mặc định
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

    /// Chuyển đổi thông tin Trace Context nội bộ thành opentelemetry::Context để liên kết các Span
    pub fn get_otel_context(&self) -> opentelemetry::Context {
        use opentelemetry::trace::{SpanContext, SpanId, TraceContextExt, TraceFlags, TraceId};

        let trace_id = TraceId::from_hex(&self.trace_id).ok();
        let span_id = SpanId::from_hex(&self.span_id).ok();

        if let (Some(tid), Some(sid)) = (trace_id, span_id) {
            let span_context = SpanContext::new(
                tid,
                sid,
                TraceFlags::SAMPLED,
                true,
                opentelemetry::trace::TraceState::default(),
            );
            // Kế thừa Span context từ remote parent trace
            opentelemetry::Context::current().with_remote_span_context(span_context)
        } else {
            opentelemetry::Context::current()
        }
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
        let hostname = get_node_hostname();

        // Định danh tài nguyên nghiệp vụ trong hệ thống giám sát tập trung
        let resource = Resource::new(vec![
            KeyValue::new("service.name", "aurora-notification-service"),
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
        let hostname = get_node_hostname();

        // Thiết lập resource attributes mô tả nguồn gốc metrics
        let resource = Resource::new(vec![
            KeyValue::new("service.name", "aurora-notification-service"),
            KeyValue::new("hostname", hostname),
        ]);

        // Xây dựng Metric Exporter theo chuẩn OTLP gRPC Tonic
        let otlp_exporter = match opentelemetry_otlp::new_exporter()
            .tonic()
            .with_endpoint(endpoint)
            .build_metrics_exporter(
                Box::new(opentelemetry_sdk::metrics::reader::DefaultAggregationSelector::new()),
                Box::new(opentelemetry_sdk::metrics::reader::DefaultTemporalitySelector::new()),
            ) {
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

    pub fn extract_context(traceparent: &str, tracestate: &str) -> Context {
        if traceparent.is_empty()
            || traceparent.len() > MAX_TRACEPARENT_BYTES
            || tracestate.len() > MAX_TRACESTATE_BYTES
        {
            return Context::new();
        }
        let mut carrier = HashMap::with_capacity(2);
        carrier.insert("traceparent".to_string(), traceparent.to_string());
        if !tracestate.is_empty() {
            carrier.insert("tracestate".to_string(), tracestate.to_string());
        }
        global::get_text_map_propagator(|propagator| {
            propagator.extract_with_context(&Context::new(), &carrier)
        })
    }

    pub fn is_valid_propagation_context(traceparent: &str, tracestate: &str) -> bool {
        if traceparent.is_empty() {
            return tracestate.is_empty();
        }
        Self::extract_context(traceparent, tracestate)
            .span()
            .span_context()
            .is_valid()
    }

    pub fn start_span_with_parent(
        name: impl Into<Cow<'static, str>>,
        kind: SpanKind,
        attributes: Vec<KeyValue>,
        parent: &Context,
    ) -> Context {
        let tracer = global::tracer("aurora-notification-service");
        let span = tracer
            .span_builder(name)
            .with_kind(kind)
            .with_attributes(attributes)
            .start_with_context(&tracer, parent);
        parent.clone().with_span(span)
    }

    pub fn start_current_span(
        name: impl Into<Cow<'static, str>>,
        kind: SpanKind,
        attributes: Vec<KeyValue>,
    ) -> Context {
        Self::start_span_with_parent(name, kind, attributes, &Context::current())
    }

    pub fn finish_span(context: &Context, error_code: Option<&str>) {
        let span = context.span();
        if let Some(error_code) = error_code {
            span.set_attribute(KeyValue::new("error.type", error_code.to_string()));
            span.set_status(Status::error(error_code.to_string()));
        } else {
            span.set_status(Status::Ok);
        }
        span.end();
    }

    pub fn inject_context(context: &Context) -> PropagationContext {
        let mut carrier = HashMap::with_capacity(2);
        global::get_text_map_propagator(|propagator| {
            propagator.inject_context(context, &mut carrier);
        });
        PropagationContext {
            traceparent: carrier.remove("traceparent").unwrap_or_default(),
            tracestate: carrier.remove("tracestate").unwrap_or_default(),
        }
    }

    pub fn get_current_trace_id() -> Option<String> {
        let context = Context::current();
        let span = context.span();
        let span_context = span.span_context();
        span_context
            .is_valid()
            .then(|| span_context.trace_id().to_string())
    }

    pub fn get_current_span_id() -> Option<String> {
        let context = Context::current();
        let span = context.span();
        let span_context = span.span_context();
        span_context
            .is_valid()
            .then(|| span_context.span_id().to_string())
    }

    /// Lấy TraceContext hiện tại của async task nếu có
    pub fn get_current_trace() -> Option<TraceContext> {
        CURRENT_TRACE.try_with(|t| t.clone()).ok()
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
    std::env::var("HOSTNAME").unwrap_or_else(|_| {
        hostname::get()
            .map(|h| h.into_string().unwrap_or_default())
            .unwrap_or_default()
    })
}
