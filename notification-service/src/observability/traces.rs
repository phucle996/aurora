use opentelemetry::propagation::{Extractor, Injector};
use opentelemetry::trace::{SpanKind, Status, TraceContextExt, Tracer};
use opentelemetry::{global, Context, KeyValue};
use std::borrow::Cow;
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

    (None, None)
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
    Context::new()
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

pub fn get_node_hostname() -> String {
    std::env::var("HOSTNAME").unwrap_or_else(|_| {
        hostname::get()
            .map(|value| value.to_string_lossy().into_owned())
            .unwrap_or_else(|_| "unknown".to_owned())
    })
}

#[cfg(test)]
mod tests {
    use super::OtelTracer;
    use opentelemetry::trace::TraceContextExt;

    #[test]
    fn rejects_malformed_or_zero_w3c_context() {
        assert!(!OtelTracer::is_valid_propagation_context(
            "00-00000000000000000000000000000000-0000000000000000-01",
            "",
        ));
        assert!(!OtelTracer::is_valid_propagation_context(
            "00-not-hex-0000000000000001-01",
            "",
        ));
        assert!(!OtelTracer::is_valid_propagation_context(
            "00-00000000000000000000000000000000-0000000000000000-01",
            "",
        ));
    }

    #[test]
    fn preserves_sample_flag() {
        let context = OtelTracer::extract_context(
            "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00",
            "",
        );
        assert!(!context.span().span_context().is_sampled());
    }
}
