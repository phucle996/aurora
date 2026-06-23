// ============================================================================
// 📂 MODULE: controlplane/internal/core/metrics/metrics.go
//            Đo Lường Nghiệp Vụ Module Core (OTel Metrics)
// ============================================================================

package coreMetric

import (
	"context"
	"sync"
	"time"

	"controlplane/pkg/constant"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ──────────────────────────────────────────────────────────────────────────────
// STANDARDIZED DOWNSTREAM KINDS (Các loại Downstream được chuẩn hóa)
// ──────────────────────────────────────────────────────────────────────────────

const (
	KindRepo              = "repo"
	KindCacheEngineL1     = "cache-engine-l1"
	KindCacheEngineL2     = "cache-engine-l2"
	KindCacheEngineFanout = "cache-engine-fanout"
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
	// serviceCallsCounter đếm tổng số lần gọi service layer Core.
	serviceCallsCounter metric.Int64Counter

	// downstreamDuration đo latency (giây) các tác vụ downstream của module Core.
	downstreamDuration metric.Float64Histogram

	// initOnce đảm bảo instruments chỉ được tạo một lần duy nhất.
	initOnce sync.Once
)

// Init khởi tạo các OTel instruments một cách tường minh từ observability/otel.
func Init(meterProvider metric.MeterProvider) {
	initOnce.Do(func() {
		meter := meterProvider.Meter("aurora-controlplane.core")

		// Counter: đếm tổng số lần gọi service Core
		serviceCallsCounter, _ = meter.Int64Counter(
			"aurora_controlplane_core_service_calls_total",
			metric.WithDescription("Total core service calls, partitioned by op and outcome."),
		)

		// Histogram: đo latency downstream Core (DB, Redis, cache)
		downstreamDuration, _ = meter.Float64Histogram(
			"aurora_controlplane_core_downstream_duration_seconds",
			metric.WithDescription("Latency in seconds of core downstream calls."),
		)
	})
}

// ServiceCall ghi nhận một lần gọi service Core.
func ServiceCall(ctx context.Context, outcome string) {
	if serviceCallsCounter != nil {
		op := constant.GetOperation(ctx)
		serviceCallsCounter.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("op", op),
				attribute.String("outcome", outcome),
			),
		)
	}
}

// Downstream ghi nhận latency của một tác vụ downstream Core.
func Downstream(ctx context.Context, kind, destination, outcome string, duration time.Duration, err error) {
	if downstreamDuration != nil {
		status := "ok"
		if err != nil {
			status = "error"
		}
		op := constant.GetOperation(ctx)
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
