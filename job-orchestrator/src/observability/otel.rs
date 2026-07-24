use crate::config::{get_node_hostname, Config};
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

const MAX_TRACEPARENT_BYTES: usize = 128;
const MAX_TRACESTATE_BYTES: usize = 512;

static METER_PROVIDER: OnceLock<SdkMeterProvider> = OnceLock::new();
static OTEL_INITIALIZED: OnceLock<()> = OnceLock::new();
static SERVICE_INSTANCE_ID: OnceLock<String> = OnceLock::new();
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

    fn resource(config: &Config) -> Resource {
        let environment =
            std::env::var("DEPLOYMENT_ENVIRONMENT").unwrap_or_else(|_| "development".to_string());
        Resource::new(vec![
            KeyValue::new("service.namespace", "aurora"),
            KeyValue::new("service.name", "aurora-job-orchestrator"),
            KeyValue::new("service.version", env!("CARGO_PKG_VERSION")),
            KeyValue::new(
                "service.instance.id",
                SERVICE_INSTANCE_ID
                    .get_or_init(|| uuid::Uuid::new_v4().to_string())
                    .clone(),
            ),
            KeyValue::new("deployment.environment.name", environment),
            KeyValue::new("aurora.zone.id", config.zone_id.clone()),
            KeyValue::new("host.name", get_node_hostname()),
        ])
    }

    fn init_tracer(config: &Config) {
        let endpoint = &config.otel_exporter_otlp_endpoint;
        let exporter = opentelemetry_otlp::new_exporter()
            .tonic()
            .with_endpoint(endpoint);

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
                    .with_resource(Self::resource(config)),
            )
            .install_batch(opentelemetry_sdk::runtime::Tokio)
        {
            Ok(_) => Logger::sys_info(
                "tracing.init",
                &format!(
                    "OTel tracer initialized; endpoint={}, root_sample_ratio={}",
                    endpoint, config.otel_trace_sample_ratio
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
        let endpoint = &config.otel_exporter_otlp_endpoint;
        let exporter = match opentelemetry_otlp::new_exporter()
            .tonic()
            .with_endpoint(endpoint)
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
            .with_interval(std::time::Duration::from_secs(15))
            .build();
        let provider = SdkMeterProvider::builder()
            .with_reader(reader)
            .with_resource(Self::resource(config))
            .build();

        global::set_meter_provider(provider.clone());
        if METER_PROVIDER.set(provider).is_err() {
            Logger::sys_error(
                "metrics.init",
                "Meter provider ownership was already installed",
                "METER_PROVIDER_DUPLICATE",
            );
            return;
        }
        Logger::sys_info(
            "metrics.init",
            &format!("OTel metrics pipeline initialized; endpoint={endpoint}"),
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
}
