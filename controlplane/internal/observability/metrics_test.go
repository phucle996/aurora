package observability

import (
	"bytes"
	"context"
	"testing"
	"time"

	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestWorkflowMetricsUseBoundedContract(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	metrics, err := NewMetrics(provider)
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}
	traceProvider := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = traceProvider.Shutdown(context.Background()) })

	ctx := pkgcontext.WithOperation(logger.WithCorrelation(context.Background()), "iam.auth.verify_credentials")
	ctx, span := traceProvider.Tracer("test").Start(ctx, "workflow")
	traceID := span.SpanContext().TraceID()
	metrics.ForModule("iam").ObserveWorkflow(ctx, ResultRejected, ReasonUnauthenticated, 15*time.Millisecond)
	span.End()

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	want := map[string]string{
		"module": "iam",
		"op":     "iam.auth.verify_credentials",
		"result": "rejected",
		"reason": "unauthenticated",
	}
	foundCounter := false
	foundHistogram := false
	foundTraceExemplar := false
	for _, scope := range collected.ScopeMetrics {
		for _, current := range scope.Metrics {
			switch current.Name {
			case "aurora_controlplane_workflow_calls_total":
				sum, ok := current.Data.(metricdata.Sum[int64])
				if !ok || len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != 1 {
					t.Fatalf("workflow counter data = %#v", current.Data)
				}
				assertMetricAttributes(t, sum.DataPoints[0].Attributes.ToSlice(), want)
				foundCounter = true
			case "aurora_controlplane_workflow_duration_seconds":
				histogram, ok := current.Data.(metricdata.Histogram[float64])
				if !ok || len(histogram.DataPoints) != 1 || histogram.DataPoints[0].Count != 1 {
					t.Fatalf("workflow histogram data = %#v", current.Data)
				}
				assertMetricAttributes(t, histogram.DataPoints[0].Attributes.ToSlice(), want)
				for _, exemplar := range histogram.DataPoints[0].Exemplars {
					if bytes.Equal(exemplar.TraceID, traceID[:]) {
						foundTraceExemplar = true
					}
				}
				foundHistogram = true
			}
		}
	}
	if !foundCounter || !foundHistogram || !foundTraceExemplar {
		t.Fatalf("workflow correlation missing: counter=%v histogram=%v exemplar=%v", foundCounter, foundHistogram, foundTraceExemplar)
	}
}

func TestWorkflowOutcomeCorrelatesNoopMetricWithActiveSpanAndLogs(t *testing.T) {
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	t.Cleanup(func() { _ = traceProvider.Shutdown(context.Background()) })

	root := logger.WithCorrelation(context.Background())
	ctx := pkgcontext.WithOperation(root, "mail.personal.consumer.update")
	ctx, span := traceProvider.Tracer("test").Start(ctx, "request")
	NewNoopMetrics().ForModule("mail").ObserveWorkflow(
		ctx,
		ResultFailure,
		ReasonUnavailable,
		20*time.Millisecond,
	)
	span.End()

	correlation, ok := logger.CorrelationFromContext(root)
	if !ok || !correlation.Observed {
		t.Fatalf("correlation = %#v, %v", correlation, ok)
	}
	if correlation.Module != "mail" || correlation.Operation != "mail.personal.consumer.update" ||
		correlation.Result != "failure" || correlation.Reason != "unavailable" {
		t.Fatalf("correlation = %#v", correlation)
	}

	ended := spanRecorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(ended))
	}
	attributes := make(map[string]string)
	for _, current := range ended[0].Attributes() {
		attributes[string(current.Key)] = current.Value.AsString()
	}
	for key, want := range map[string]string{
		"aurora.module":    "mail",
		"aurora.operation": "mail.personal.consumer.update",
		"aurora.result":    "failure",
		"aurora.reason":    "unavailable",
	} {
		if attributes[key] != want {
			t.Fatalf("span attribute %s = %q, want %q", key, attributes[key], want)
		}
	}
	if ended[0].Status().Code != codes.Error {
		t.Fatalf("span status = %v, want Error", ended[0].Status().Code)
	}
}

func TestDependencyObservationUsesMetricDimensionsOnActiveSpan(t *testing.T) {
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	t.Cleanup(func() { _ = traceProvider.Shutdown(context.Background()) })

	ctx := pkgcontext.WithOperation(context.Background(), "storage.bucket.create")
	ctx, span := traceProvider.Tracer("test").Start(ctx, "redis.get")
	NewNoopMetrics().ObserveDependency(
		ctx,
		"redis",
		"get",
		ResultRejected,
		ReasonEmpty,
		5*time.Millisecond,
	)
	span.End()

	ended := spanRecorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(ended))
	}
	attributes := make(map[string]string)
	for _, current := range ended[0].Attributes() {
		attributes[string(current.Key)] = current.Value.AsString()
	}
	for key, want := range map[string]string{
		"aurora.module":               "storage",
		"aurora.operation":            "storage.bucket.create",
		"aurora.dependency.system":    "redis",
		"aurora.dependency.operation": "get",
		"aurora.dependency.result":    "rejected",
		"aurora.dependency.reason":    "empty",
	} {
		if attributes[key] != want {
			t.Fatalf("span attribute %s = %q, want %q", key, attributes[key], want)
		}
	}
}

func TestMetricNormalizationRejectsUnboundedLabels(t *testing.T) {
	if got := normalizeOperation("iam.user/" + string(make([]byte, 129))); got != "unknown" {
		t.Fatalf("normalizeOperation() = %q, want unknown", got)
	}
	if got := normalizeRoute(""); got != "__unmatched__" {
		t.Fatalf("normalizeRoute(empty) = %q", got)
	}
	if got := normalizeRoute("/"); got != "/" {
		t.Fatalf("normalizeRoute(root) = %q", got)
	}
	if got := normalizeStatusCode("999"); got != "000" {
		t.Fatalf("normalizeStatusCode() = %q", got)
	}
	if gotResult, gotReason := normalizeResultReason(ResultSuccess, ReasonInternal); gotResult != ResultSuccess || gotReason != ReasonNone {
		t.Fatalf("normalizeResultReason() = %q/%q", gotResult, gotReason)
	}
}

func assertMetricAttributes(t *testing.T, attributes []attribute.KeyValue, want map[string]string) {
	t.Helper()
	got := make(map[string]string, len(attributes))
	for _, current := range attributes {
		got[string(current.Key)] = current.Value.AsString()
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("attribute %q = %q, want %q (all=%v)", key, got[key], value, got)
		}
	}
}
