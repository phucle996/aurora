use crate::config::Config;
use crate::observability::logger::Logger;
use opentelemetry::trace::{SpanKind, Status, TraceContextExt, Tracer};
use opentelemetry::{global, Context, KeyValue};
use opentelemetry_otlp::WithExportConfig;
use opentelemetry_sdk::{
    metrics::{PeriodicReader, SdkMeterProvider},
    propagation::TraceContextPropagator,
    trace::{self, BatchConfigBuilder, Sampler},
    Resource,
};
use std::borrow::Cow;
use std::collections::HashMap;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::OnceLock;
use std::time::Duration;

const MAX_TRACEPARENT_BYTES: usize = 128;
const MAX_TRACESTATE_BYTES: usize = 512;
const MAX_SPAN_NAME_BYTES: usize = 128;

static METER_PROVIDER: OnceLock<SdkMeterProvider> = OnceLock::new();
static OTEL_INITIALIZED: OnceLock<()> = OnceLock::new();
static OTEL_STOPPED: AtomicBool = AtomicBool::new(false);

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct PropagationContext {
    pub traceparent: String,
    pub tracestate: String,
}

pub struct OtelTracer;

impl OtelTracer {
    pub fn init(config: &Config) {
        if OTEL_INITIALIZED.set(()).is_err() {
            Logger::sys_warn(
                "observability.init",
                "OpenTelemetry initialization was requested more than once; duplicate ignored",
                "OTEL_ALREADY_INITIALIZED",
            );
            return;
        }

        global::set_text_map_propagator(TraceContextPropagator::new());
        Self::init_tracer(config);
        Self::init_metrics(config);
    }

    fn resource() -> Resource {
        Resource::new(vec![
            KeyValue::new("service.namespace", "aurora"),
            KeyValue::new("service.name", "aurora-job-orchestrator"),
            KeyValue::new("service.version", env!("CARGO_PKG_VERSION")),
            KeyValue::new("service.instance.id", Logger::boot_id().to_string()),
            KeyValue::new(
                "deployment.environment.name",
                Logger::deployment_environment().to_string(),
            ),
            // Job Orchestrator is a Central multi-Zone process. Zone belongs on a job span,
            // never on its Resource, otherwise one pod reports a false global Zone identity.
            KeyValue::new("aurora.component.scope", "central"),
            KeyValue::new("host.name", Logger::node_id().to_string()),
            KeyValue::new("process.pid", i64::from(std::process::id())),
        ])
    }

    fn init_tracer(config: &Config) {
        let export_timeout = Duration::from_secs(config.otel_export_timeout_secs);
        let exporter = opentelemetry_otlp::new_exporter()
            .tonic()
            .with_endpoint(&config.otel_exporter_otlp_endpoint)
            .with_timeout(export_timeout);

        match opentelemetry_otlp::new_pipeline()
            .tracing()
            .with_exporter(exporter)
            .with_batch_config(BatchConfigBuilder::default().build())
            .with_trace_config(
                trace::Config::default()
                    .with_sampler(Sampler::ParentBased(Box::new(Sampler::TraceIdRatioBased(
                        config.otel_trace_sample_ratio,
                    ))))
                    .with_max_attributes_per_span(32)
                    .with_max_events_per_span(32)
                    .with_max_links_per_span(32)
                    .with_max_attributes_per_event(16)
                    .with_max_attributes_per_link(8)
                    .with_resource(Self::resource()),
            )
            .install_batch(opentelemetry_sdk::runtime::Tokio)
        {
            Ok(_) => Logger::sys_info(
                "tracing.init",
                &format!(
                    "OTel tracer initialized; root_sample_ratio={}, export_timeout_secs={}",
                    config.otel_trace_sample_ratio, config.otel_export_timeout_secs
                ),
            ),
            Err(error) => Logger::sys_error(
                "tracing.init",
                &format!("Failed to initialize OTel tracing pipeline: {error}"),
                "OTEL_INIT_ERROR",
            ),
        }
    }

    fn init_metrics(config: &Config) {
        let exporter = match opentelemetry_otlp::new_exporter()
            .tonic()
            .with_endpoint(&config.otel_exporter_otlp_endpoint)
            .with_timeout(Duration::from_secs(config.otel_export_timeout_secs))
            .build_metrics_exporter(
                Box::new(opentelemetry_sdk::metrics::reader::DefaultAggregationSelector::new()),
                Box::new(opentelemetry_sdk::metrics::reader::DefaultTemporalitySelector::new()),
            ) {
            Ok(exporter) => exporter,
            Err(error) => {
                Logger::sys_error(
                    "metrics.init",
                    &format!("Failed to build OTel metrics exporter: {error}"),
                    "METRICS_EXPORTER_ERROR",
                );
                return;
            }
        };

        let reader = PeriodicReader::builder(exporter, opentelemetry_sdk::runtime::Tokio)
            .with_interval(Duration::from_secs(config.otel_metric_export_interval_secs))
            .with_timeout(Duration::from_secs(config.otel_export_timeout_secs))
            .build();
        let provider = SdkMeterProvider::builder()
            .with_reader(reader)
            .with_resource(Self::resource())
            .build();

        if METER_PROVIDER.set(provider.clone()).is_err() {
            Logger::sys_error(
                "metrics.init",
                "Meter provider ownership was already installed",
                "METER_PROVIDER_DUPLICATE",
            );
            return;
        }
        global::set_meter_provider(provider);
        Logger::sys_info(
            "metrics.init",
            &format!(
                "OTel metrics pipeline initialized; interval_secs={}, export_timeout_secs={}",
                config.otel_metric_export_interval_secs, config.otel_export_timeout_secs
            ),
        );
    }

    #[allow(dead_code)]
    pub fn stop() {
        if OTEL_STOPPED.swap(true, Ordering::AcqRel) {
            return;
        }
        global::shutdown_tracer_provider();
        if let Some(provider) = METER_PROVIDER.get() {
            if let Err(error) = provider.shutdown() {
                Logger::sys_error(
                    "metrics.stop",
                    &format!("Failed to shutdown metrics provider: {error}"),
                    "METRICS_SHUTDOWN_ERROR",
                );
            }
        }
        Logger::sys_info(
            "observability.stop",
            "OpenTelemetry providers completed bounded shutdown",
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
        let tracer = global::tracer("aurora-job-orchestrator");
        let span = tracer
            .span_builder(bounded_span_name(name.into()))
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
            let error_code = stable_error_code(error_code);
            span.set_attribute(KeyValue::new("error.type", error_code));
            span.set_status(Status::error(error_code));
        } else {
            span.set_status(Status::Ok);
        }
        span.end();
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
}

fn bounded_span_name(name: Cow<'static, str>) -> Cow<'static, str> {
    if name.len() <= MAX_SPAN_NAME_BYTES {
        return name;
    }
    const SUFFIX: &str = "...[truncated]";
    let mut boundary = MAX_SPAN_NAME_BYTES.saturating_sub(SUFFIX.len());
    while boundary > 0 && !name.is_char_boundary(boundary) {
        boundary -= 1;
    }
    Cow::Owned(format!("{}{SUFFIX}", &name[..boundary]))
}

fn stable_error_code(value: &str) -> &'static str {
    if !value.is_empty()
        && value.len() <= 128
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b'-' | b'.'))
    {
        // Callers currently pass static taxonomy values. Returning a static fallback for any
        // future raw error prevents unbounded error.type cardinality.
        match value {
            "KAFKA_COMMAND_PUBLISH_FAILED" => "KAFKA_COMMAND_PUBLISH_FAILED",
            "KAFKA_RECONCILE_PUBLISH_FAILED" => "KAFKA_RECONCILE_PUBLISH_FAILED",
            "KAFKA_RESULT_SETTLEMENT_FAILED" => "KAFKA_RESULT_SETTLEMENT_FAILED",
            "JOB_RESULT_PROCESS_FAILED" => "JOB_RESULT_PROCESS_FAILED",
            "POSTGRES_JOB_RESULT_UPDATE_FAILED" => "POSTGRES_JOB_RESULT_UPDATE_FAILED",
            "REDIS_NOTIFICATION_XADD_FAILED" => "REDIS_NOTIFICATION_XADD_FAILED",
            _ => "UNCLASSIFIED_ERROR",
        }
    } else {
        "UNCLASSIFIED_ERROR"
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use opentelemetry::trace::{SpanContext, SpanId, TraceFlags, TraceId, TraceState};

    #[test]
    fn w3c_round_trip_preserves_unsampled_flag_and_parent() {
        global::set_text_map_propagator(TraceContextPropagator::new());
        let remote = SpanContext::new(
            TraceId::from_hex("4bf92f3577b34da6a3ce929d0e0e4736").unwrap(),
            SpanId::from_hex("00f067aa0ba902b7").unwrap(),
            TraceFlags::default(),
            true,
            TraceState::default(),
        );
        let source = Context::new().with_remote_span_context(remote.clone());
        let carrier = OtelTracer::inject_context(&source);
        let extracted = OtelTracer::extract_context(&carrier.traceparent, &carrier.tracestate);
        let extracted_span = extracted.span();

        assert_eq!(extracted_span.span_context().trace_id(), remote.trace_id());
        assert_eq!(extracted_span.span_context().span_id(), remote.span_id());
        assert!(!extracted_span.span_context().is_sampled());
    }

    #[test]
    fn oversized_context_is_rejected() {
        let extracted = OtelTracer::extract_context(&"x".repeat(129), "");
        assert!(!extracted.span().span_context().is_valid());
    }

    #[test]
    fn empty_context_is_rolling_compatible_but_partial_context_is_rejected() {
        assert!(OtelTracer::is_valid_propagation_context("", ""));
        assert!(!OtelTracer::is_valid_propagation_context(
            "",
            "vendor=value"
        ));
        assert!(!OtelTracer::is_valid_propagation_context("00-invalid", ""));
    }

    #[test]
    fn span_names_and_error_taxonomy_are_bounded() {
        let name = bounded_span_name(Cow::Owned("ế".repeat(256)));
        assert!(name.len() <= MAX_SPAN_NAME_BYTES);
        assert!(name.ends_with("...[truncated]"));
        assert_eq!(
            stable_error_code("raw error with customer id"),
            "UNCLASSIFIED_ERROR"
        );
    }
}
