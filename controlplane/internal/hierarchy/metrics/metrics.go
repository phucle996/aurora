// ============================================================================
// 📂 MODULE: controlplane/internal/hierarchy/metrics/metrics.go
//            Đo Lường Nghiệp Vụ Module Hierarchy (OTel Metrics)
// ============================================================================

package hierarchyMetrics

import (
	"context"
	"sync"
	"time"

	"controlplane/pkg/context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ──────────────────────────────────────────────────────────────────────────────
// STANDARDIZED DOWNSTREAM KINDS (Các loại Downstream được chuẩn hóa)
// ──────────────────────────────────────────────────────────────────────────────

const (
	KindRepo               = "repo"
	KindCacheEngineL1      = "cache-engine-l1"
	KindCacheEngineL2      = "cache-engine-l2"
	KindCacheEngineFanout  = "cache-engine-fanout"
	KindCacheEngineExecute = "cache-engine-execute"
)

// ──────────────────────────────────────────────────────────────────────────────
// STANDARDIZED SERVICE CALL OUTCOMES (Các loại Outcome được chuẩn hóa)
// ──────────────────────────────────────────────────────────────────────────────

const (
	OutcomeSuccess            = "success"
	OutcomeFailure            = "failure"
	OutcomeFailureUnknown     = "failure_unknown"
	OutcomePreConditionFailed = "precondition_failed"
	OutcomeInvalidCredential  = "invalid_credential"
	OutcomeLockBusy           = "lock_busy"
)

var (
	// serviceCallsCounter đếm tổng số lần gọi service layer Hierarchy.
	serviceCallsCounter metric.Int64Counter

	// downstreamDuration đo latency các downstream của Hierarchy.
	downstreamDuration metric.Float64Histogram

	// initOnce đảm bảo instruments chỉ được tạo một lần duy nhất.
	initOnce sync.Once
)

// Init khởi tạo các OTel instruments một cách tường minh từ observability/otel.
func Init(meterProvider metric.MeterProvider) {
	initOnce.Do(func() {
		meter := meterProvider.Meter("aurora-controlplane.hierarchy")

		// Counter: đếm tổng số lần gọi service Hierarchy.
		serviceCallsCounter, _ = meter.Int64Counter(
			"aurora_controlplane_hierarchy_service_calls_total",
			metric.WithDescription("Total hierarchy service calls, partitioned by op and outcome."),
		)

		// Histogram: đo latency downstream Hierarchy (DB, Redis, cache).
		downstreamDuration, _ = meter.Float64Histogram(
			"aurora_controlplane_hierarchy_downstream_duration_seconds",
			metric.WithDescription("Latency in seconds of hierarchy downstream calls."),
		)
	})
}

// ServiceCall ghi nhận một lần gọi service Hierarchy.
func ServiceCall(ctx context.Context, outcome string) {
	if serviceCallsCounter != nil {
		op := pkgcontext.GetOperation(ctx)
		serviceCallsCounter.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("op", op),
				attribute.String("outcome", outcome),
			),
		)
	}
}

// Downstream ghi nhận latency của một tác vụ downstream Hierarchy.
func Downstream(ctx context.Context, kind, destination, outcome string, duration time.Duration, err error) {
	if downstreamDuration != nil {
		status := "ok"
		if err != nil {
			status = "error"
		}
		op := pkgcontext.GetOperation(ctx)
		downstreamDuration.Record(ctx, duration.Seconds(),
			metric.WithAttributes(
				attribute.String("kind", kind),
				attribute.String("op", op),
				attribute.String("destination", destination),
				attribute.String("outcome", outcome),
				attribute.String("status", status),
			),
		)
	}
}
