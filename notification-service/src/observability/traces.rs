use opentelemetry::propagation::{Extractor, Injector};
use opentelemetry::trace::{
    SpanId, SpanKind, Status, TraceContextExt, TraceFlags, TraceId, TraceState, Tracer,
};
use opentelemetry::{global, Context, KeyValue};
use std::borrow::Cow;
use tokio::task_local;
use tracing_opentelemetry::OpenTelemetrySpanExt;

const MAX_TRACEPARENT_BYTES: usize = 128;
const MAX_TRACESTATE_BYTES: usize = 512;
const MAX_SPAN_NAME_BYTES: usize = 128;
const MAX_ATTRIBUTES_PER_SPAN: usize = 32;

#[derive(Default)]
pub struct PropagationContext {
    pub traceparent: String,
    pub tracestate: String,
}

impl Injector for PropagationContext {
    fn set(&mut self, key: &str, value: String) {
        match key {
            "traceparent" => self.traceparent = value,
            "tracestate" => self.tracestate = value,
            _ => {}
        }
    }
}

struct BorrowedPropagationContext<'a> {
    traceparent: &'a str,
    tracestate: &'a str,
}

impl Extractor for BorrowedPropagationContext<'_> {
    fn get(&self, key: &str) -> Option<&str> {
        match key {
            "traceparent" if !self.traceparent.is_empty() => Some(self.traceparent),
            "tracestate" if !self.tracestate.is_empty() => Some(self.tracestate),
            _ => None,
        }
    }

    fn keys(&self) -> Vec<&str> {
        match (self.traceparent.is_empty(), self.tracestate.is_empty()) {
            (false, false) => vec!["traceparent", "tracestate"],
            (false, true) => vec!["traceparent"],
            (true, false) => vec!["tracestate"],
            (true, true) => Vec::new(),
        }
    }
}

#[derive(Clone, Debug)]
pub struct TraceContext {
    pub trace_id: String,
    pub span_id: String,
    sampled: bool,
}

impl TraceContext {
    pub fn parse(traceparent: &str) -> Option<Self> {
        if traceparent.len() > MAX_TRACEPARENT_BYTES {
            return None;
        }

        if traceparent.len() == 32 {
            let trace_id = TraceId::from_hex(traceparent).ok()?;
            if trace_id == TraceId::INVALID {
                return None;
            }
            return Some(Self {
                trace_id: trace_id.to_string(),
                span_id: random_span_id().to_string(),
                sampled: true,
            });
        }

        let mut parts = traceparent.split('-');
        let version = parts.next()?;
        let trace_id = parts.next()?;
        let span_id = parts.next()?;
        let flags = parts.next()?;
        if parts.next().is_some()
            || version != "00"
            || trace_id.len() != 32
            || span_id.len() != 16
            || flags.len() != 2
        {
            return None;
        }
        let trace_id = TraceId::from_hex(trace_id).ok()?;
        let span_id = SpanId::from_hex(span_id).ok()?;
        let flags = u8::from_str_radix(flags, 16).ok()?;
        if trace_id == TraceId::INVALID || span_id == SpanId::INVALID {
            return None;
        }

        Some(Self {
            trace_id: trace_id.to_string(),
            span_id: span_id.to_string(),
            sampled: flags & TraceFlags::SAMPLED.to_u8() != 0,
        })
    }

    pub fn new_random() -> Self {
        Self {
            trace_id: random_trace_id().to_string(),
            span_id: random_span_id().to_string(),
            sampled: true,
        }
    }

    pub fn get_otel_context(&self) -> Context {
        let (Ok(trace_id), Ok(span_id)) = (
            TraceId::from_hex(&self.trace_id),
            SpanId::from_hex(&self.span_id),
        ) else {
            return Context::new();
        };
        if trace_id == TraceId::INVALID || span_id == SpanId::INVALID {
            return Context::new();
        }

        let flags = if self.sampled {
            TraceFlags::SAMPLED
        } else {
            TraceFlags::default()
        };
        Context::new().with_remote_span_context(opentelemetry::trace::SpanContext::new(
            trace_id,
            span_id,
            flags,
            true,
            TraceState::default(),
        ))
    }
}

task_local! {
    pub static CURRENT_TRACE: TraceContext;
}

pub struct OtelTracer;

impl OtelTracer {
    pub fn extract_context(traceparent: &str, tracestate: &str) -> Context {
        if traceparent.is_empty()
            || traceparent.len() > MAX_TRACEPARENT_BYTES
            || tracestate.len() > MAX_TRACESTATE_BYTES
        {
            return Context::new();
        }
        let carrier = BorrowedPropagationContext {
            traceparent,
            tracestate,
        };
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
        let tracer = global::tracer(super::SERVICE_NAME);
        let span = tracer
            .span_builder(bounded_span_name(name.into()))
            .with_kind(kind)
            .with_attributes(attributes.into_iter().take(MAX_ATTRIBUTES_PER_SPAN))
            .start_with_context(&tracer, parent);
        parent.clone().with_span(span)
    }

    pub fn start_current_span(
        name: impl Into<Cow<'static, str>>,
        kind: SpanKind,
        attributes: Vec<KeyValue>,
    ) -> Context {
        let parent = current_parent_context();
        Self::start_span_with_parent(name, kind, attributes, &parent)
    }

    pub fn finish_span(context: &Context, error_code: Option<&str>) {
        let span = context.span();
        if let Some(error_code) = error_code {
            span.set_attribute(KeyValue::new("error.type", error_code.to_owned()));
            span.set_status(Status::error(error_code.to_owned()));
        } else {
            span.set_status(Status::Ok);
        }
        span.end();
    }

    pub fn inject_context(context: &Context) -> PropagationContext {
        let mut carrier = PropagationContext::default();
        global::get_text_map_propagator(|propagator| {
            propagator.inject_context(context, &mut carrier);
        });
        carrier
    }

    pub fn get_current_trace() -> Option<TraceContext> {
        CURRENT_TRACE.try_with(Clone::clone).ok()
    }
}

pub(crate) fn current_span_identifiers() -> (Option<String>, Option<String>) {
    let current = Context::current();
    if let Some(ids) = identifiers_from_context(&current) {
        return (Some(ids.0), Some(ids.1));
    }

    let tracing_context = tracing::Span::current().context();
    if let Some(ids) = identifiers_from_context(&tracing_context) {
        return (Some(ids.0), Some(ids.1));
    }

    CURRENT_TRACE
        .try_with(|value| (Some(value.trace_id.clone()), Some(value.span_id.clone())))
        .unwrap_or((None, None))
}

fn identifiers_from_context(context: &Context) -> Option<(String, String)> {
    let span = context.span();
    let span_context = span.span_context();
    span_context.is_valid().then(|| {
        (
            span_context.trace_id().to_string(),
            span_context.span_id().to_string(),
        )
    })
}

fn current_parent_context() -> Context {
    let current = Context::current();
    if current.span().span_context().is_valid() {
        return current;
    }
    let tracing_context = tracing::Span::current().context();
    if tracing_context.span().span_context().is_valid() {
        return tracing_context;
    }
    CURRENT_TRACE
        .try_with(TraceContext::get_otel_context)
        .unwrap_or_else(|_| Context::new())
}

fn bounded_span_name(name: Cow<'static, str>) -> Cow<'static, str> {
    if name.len() <= MAX_SPAN_NAME_BYTES {
        return name;
    }
    let mut end = MAX_SPAN_NAME_BYTES;
    while !name.is_char_boundary(end) {
        end -= 1;
    }
    Cow::Owned(name[..end].to_owned())
}

fn random_trace_id() -> TraceId {
    let bytes = *uuid::Uuid::new_v4().as_bytes();
    TraceId::from_bytes(bytes)
}

fn random_span_id() -> SpanId {
    let uuid = uuid::Uuid::new_v4();
    let mut bytes = [0_u8; 8];
    bytes.copy_from_slice(&uuid.as_bytes()[..8]);
    SpanId::from_bytes(bytes)
}

pub fn get_node_hostname() -> String {
    std::env::var("HOSTNAME").unwrap_or_else(|_| {
        hostname::get()
            .map(|value| value.to_string_lossy().into_owned())
            .unwrap_or_else(|_| "unknown".to_owned())
    })
}

#[cfg(test)]
mod tests {
    use super::{OtelTracer, TraceContext};
    use opentelemetry::trace::TraceContextExt;

    #[test]
    fn rejects_malformed_or_zero_w3c_context() {
        assert!(
            TraceContext::parse("00-00000000000000000000000000000000-0000000000000000-01")
                .is_none()
        );
        assert!(TraceContext::parse("00-not-hex-0000000000000001-01").is_none());
        assert!(!OtelTracer::is_valid_propagation_context(
            "00-00000000000000000000000000000000-0000000000000000-01",
            ""
        ));
    }

    #[test]
    fn preserves_sample_flag() {
        let context =
            TraceContext::parse("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00")
                .expect("valid traceparent");
        assert!(!context
            .get_otel_context()
            .span()
            .span_context()
            .is_sampled());
    }
}
