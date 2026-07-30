package observability

import (
	"context"
	"strings"
	"time"

	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type Result string

const (
	ResultSuccess  Result = "success"
	ResultRejected Result = "rejected"
	ResultFailure  Result = "failure"
)

type Reason string

const (
	ReasonNone               Reason = "none"
	ReasonInvalidArgument    Reason = "invalid_argument"
	ReasonNotFound           Reason = "not_found"
	ReasonAlreadyExists      Reason = "already_exists"
	ReasonConflict           Reason = "conflict"
	ReasonPreconditionFailed Reason = "precondition_failed"
	ReasonInvalidTransition  Reason = "invalid_transition"
	ReasonUnauthenticated    Reason = "unauthenticated"
	ReasonForbidden          Reason = "forbidden"
	ReasonRateLimited        Reason = "rate_limited"
	ReasonBusy               Reason = "busy"
	ReasonEmpty              Reason = "empty"
	ReasonConstraint         Reason = "constraint"
	ReasonTimeout            Reason = "timeout"
	ReasonCanceled           Reason = "canceled"
	ReasonUnavailable        Reason = "unavailable"
	ReasonInternal           Reason = "internal"
)

type WorkflowRecorder interface {
	ObserveWorkflow(context.Context, Result, Reason, time.Duration)
}

type DependencyRecorder interface {
	ObserveDependency(context.Context, string, string, Result, Reason, time.Duration)
}

type CacheRecorder interface {
	ObserveCache(context.Context, string, string, string, string)
}

type Metrics struct {
	httpRequests       metric.Int64Counter
	httpDuration       metric.Float64Histogram
	httpInFlight       metric.Int64UpDownCounter
	workflowCalls      metric.Int64Counter
	workflowDuration   metric.Float64Histogram
	dependencyCalls    metric.Int64Counter
	dependencyDuration metric.Float64Histogram
	cacheOperations    metric.Int64Counter
	timeDrift          metric.Float64Gauge
	timeSyncState      metric.Float64Gauge
}

type moduleRecorder struct {
	metrics *Metrics
	module  string
}

type noopRecorder struct{}

var timeSyncStates = [...]string{"ok", "warning", "critical", "unknown"}

func NewMetrics(provider metric.MeterProvider) (*Metrics, error) {
	meter := provider.Meter("aurora-controlplane")

	httpRequests, err := meter.Int64Counter(
		"aurora_controlplane_http_requests_total",
		metric.WithDescription("Controlplane HTTP requests by method, route and status code."),
	)
	if err != nil {
		return nil, err
	}
	httpDuration, err := meter.Float64Histogram(
		"aurora_controlplane_http_request_duration_seconds",
		metric.WithDescription("Controlplane HTTP request latency."),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}
	httpInFlight, err := meter.Int64UpDownCounter(
		"aurora_controlplane_http_in_flight_requests",
		metric.WithDescription("Controlplane HTTP requests currently executing."),
	)
	if err != nil {
		return nil, err
	}
	workflowCalls, err := meter.Int64Counter(
		"aurora_controlplane_workflow_calls_total",
		metric.WithDescription("Controlplane workflow calls by module, operation, result and reason."),
	)
	if err != nil {
		return nil, err
	}
	workflowDuration, err := meter.Float64Histogram(
		"aurora_controlplane_workflow_duration_seconds",
		metric.WithDescription("Controlplane workflow latency."),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}
	dependencyCalls, err := meter.Int64Counter(
		"aurora_controlplane_dependency_calls_total",
		metric.WithDescription("Controlplane dependency calls by workflow and bounded dependency outcome."),
	)
	if err != nil {
		return nil, err
	}
	dependencyDuration, err := meter.Float64Histogram(
		"aurora_controlplane_dependency_duration_seconds",
		metric.WithDescription("Controlplane dependency latency."),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}
	cacheOperations, err := meter.Int64Counter(
		"aurora_controlplane_cache_operations_total",
		metric.WithDescription("Controlplane cache operations by layer, bounded namespace, operation and result."),
	)
	if err != nil {
		return nil, err
	}
	timeDrift, err := meter.Float64Gauge(
		"aurora_controlplane_system_time_drift_seconds",
		metric.WithDescription("Absolute system time drift from the configured time source."),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}
	timeSyncState, err := meter.Float64Gauge(
		"aurora_controlplane_system_time_sync_state",
		metric.WithDescription("Controlplane time synchronization state as a one-hot gauge."),
	)
	if err != nil {
		return nil, err
	}

	return &Metrics{
		httpRequests:       httpRequests,
		httpDuration:       httpDuration,
		httpInFlight:       httpInFlight,
		workflowCalls:      workflowCalls,
		workflowDuration:   workflowDuration,
		dependencyCalls:    dependencyCalls,
		dependencyDuration: dependencyDuration,
		cacheOperations:    cacheOperations,
		timeDrift:          timeDrift,
		timeSyncState:      timeSyncState,
	}, nil
}

func NewNoopMetrics() *Metrics {
	return &Metrics{}
}

func NewNoopWorkflowRecorder() WorkflowRecorder {
	return noopRecorder{}
}

func NewNoopDependencyRecorder() DependencyRecorder {
	return noopRecorder{}
}

func NewNoopCacheRecorder() CacheRecorder {
	return noopRecorder{}
}

func (m *Metrics) Enabled() bool {
	return m != nil && m.httpRequests != nil
}

func (m *Metrics) ForModule(module string) WorkflowRecorder {
	return moduleRecorder{metrics: m, module: normalizeMetricToken(module, 32)}
}

func (m *Metrics) ObserveHTTPRequest(ctx context.Context, method, route, statusCode string, duration time.Duration) {
	if m == nil || m.httpRequests == nil || m.httpDuration == nil {
		return
	}
	attrs := metric.WithAttributes(
		attribute.String("method", normalizeHTTPMethod(method)),
		attribute.String("route", normalizeRoute(route)),
		attribute.String("status_code", normalizeStatusCode(statusCode)),
	)
	m.httpRequests.Add(ctx, 1, attrs)
	m.httpDuration.Record(ctx, duration.Seconds(), attrs)
}

func (m *Metrics) AddHTTPInFlight(ctx context.Context, method string, delta int64) {
	if m == nil || m.httpInFlight == nil {
		return
	}
	m.httpInFlight.Add(ctx, delta, metric.WithAttributes(
		attribute.String("method", normalizeHTTPMethod(method)),
	))
}

func (r moduleRecorder) ObserveWorkflow(ctx context.Context, result Result, reason Reason, duration time.Duration) {
	result, reason = normalizeResultReason(result, reason)
	operation := normalizeOperation(pkgcontext.GetOperation(ctx))
	logger.SetCorrelationOutcome(ctx, r.module, operation, string(result), string(reason))
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		span.SetAttributes(
			attribute.String("aurora.module", r.module),
			attribute.String("aurora.operation", operation),
			attribute.String("aurora.result", string(result)),
			attribute.String("aurora.reason", string(reason)),
		)
		if result == ResultFailure {
			span.SetStatus(codes.Error, string(reason))
		}
	}
	if r.metrics == nil || r.metrics.workflowCalls == nil || r.metrics.workflowDuration == nil {
		return
	}
	attrs := metric.WithAttributes(
		attribute.String("module", r.module),
		attribute.String("op", operation),
		attribute.String("result", string(result)),
		attribute.String("reason", string(reason)),
	)
	r.metrics.workflowCalls.Add(ctx, 1, attrs)
	r.metrics.workflowDuration.Record(ctx, duration.Seconds(), attrs)
}

func (m *Metrics) ObserveDependency(
	ctx context.Context,
	system string,
	operation string,
	result Result,
	reason Reason,
	duration time.Duration,
) {
	result, reason = normalizeResultReason(result, reason)
	op := normalizeOperation(pkgcontext.GetOperation(ctx))
	module := moduleFromOperation(op)
	system = normalizeMetricToken(system, 32)
	operation = normalizeMetricToken(operation, 64)

	// The adapter owns the active client span. Keep its bounded workflow and
	// dependency dimensions identical to the metric sample without putting IDs
	// or provider errors in trace attributes.
	if span := trace.SpanFromContext(ctx); span.IsRecording() {
		span.SetAttributes(
			attribute.String("aurora.module", module),
			attribute.String("aurora.operation", op),
			attribute.String("aurora.dependency.system", system),
			attribute.String("aurora.dependency.operation", operation),
			attribute.String("aurora.dependency.result", string(result)),
			attribute.String("aurora.dependency.reason", string(reason)),
		)
	}
	if m == nil || m.dependencyCalls == nil || m.dependencyDuration == nil {
		return
	}
	attrs := metric.WithAttributes(
		attribute.String("module", module),
		attribute.String("op", op),
		attribute.String("system", system),
		attribute.String("operation", operation),
		attribute.String("result", string(result)),
		attribute.String("reason", string(reason)),
	)
	m.dependencyCalls.Add(ctx, 1, attrs)
	m.dependencyDuration.Record(ctx, duration.Seconds(), attrs)
}

func (m *Metrics) ObserveCache(ctx context.Context, layer, namespace, operation, result string) {
	if m == nil || m.cacheOperations == nil {
		return
	}
	m.cacheOperations.Add(ctx, 1, metric.WithAttributes(
		attribute.String("layer", normalizeMetricToken(layer, 16)),
		attribute.String("namespace", normalizeMetricToken(namespace, 64)),
		attribute.String("operation", normalizeMetricToken(operation, 32)),
		attribute.String("result", normalizeMetricToken(result, 16)),
	))
}

func (m *Metrics) ObserveTimeDrift(ctx context.Context, seconds float64, state string) {
	if m == nil || m.timeDrift == nil || m.timeSyncState == nil {
		return
	}
	m.timeDrift.Record(ctx, seconds)
	for _, knownState := range timeSyncStates {
		value := 0.0
		if state == knownState {
			value = 1
		}
		m.timeSyncState.Record(ctx, value, metric.WithAttributes(attribute.String("state", knownState)))
	}
}

func (noopRecorder) ObserveWorkflow(context.Context, Result, Reason, time.Duration) {}

func (noopRecorder) ObserveDependency(context.Context, string, string, Result, Reason, time.Duration) {
}

func (noopRecorder) ObserveCache(context.Context, string, string, string, string) {}

func normalizeResultReason(result Result, reason Reason) (Result, Reason) {
	switch result {
	case ResultSuccess:
		return ResultSuccess, ReasonNone
	case ResultRejected:
		switch reason {
		case ReasonInvalidArgument, ReasonNotFound, ReasonAlreadyExists, ReasonConflict,
			ReasonPreconditionFailed, ReasonInvalidTransition, ReasonUnauthenticated,
			ReasonForbidden, ReasonRateLimited, ReasonBusy, ReasonEmpty, ReasonConstraint:
			return result, reason
		default:
			return ResultRejected, ReasonConflict
		}
	case ResultFailure:
		switch reason {
		case ReasonTimeout, ReasonCanceled, ReasonUnavailable, ReasonInternal:
			return result, reason
		default:
			return ResultFailure, ReasonInternal
		}
	default:
		return ResultFailure, ReasonInternal
	}
}

func normalizeOperation(operation string) string {
	operation = strings.ToLower(strings.TrimSpace(operation))
	if operation == "" || operation == "unknown" || len(operation) > 128 {
		return "unknown"
	}
	for _, value := range operation {
		if (value < 'a' || value > 'z') && (value < '0' || value > '9') && value != '.' && value != '_' {
			return "unknown"
		}
	}
	return operation
}

func moduleFromOperation(operation string) string {
	if index := strings.IndexByte(operation, '.'); index > 0 {
		return normalizeMetricToken(operation[:index], 32)
	}
	return "unknown"
}

func normalizeMetricToken(value string, maxLength int) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || len(value) > maxLength {
		return "unknown"
	}
	for _, current := range value {
		if (current < 'a' || current > 'z') && (current < '0' || current > '9') && current != '_' && current != '-' {
			return "unknown"
		}
	}
	return value
}

func normalizeHTTPMethod(method string) string {
	method = strings.ToUpper(strings.TrimSpace(method))
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return method
	default:
		return "OTHER"
	}
}

func normalizeRoute(route string) string {
	route = strings.TrimSpace(route)
	if route == "" {
		return "__unmatched__"
	}
	if len(route) > 256 {
		return "__unmatched__"
	}
	return route
}

func normalizeStatusCode(statusCode string) string {
	statusCode = strings.TrimSpace(statusCode)
	if len(statusCode) != 3 || statusCode[0] < '1' || statusCode[0] > '5' {
		return "000"
	}
	for index := 1; index < len(statusCode); index++ {
		if statusCode[index] < '0' || statusCode[index] > '9' {
			return "000"
		}
	}
	return statusCode
}
