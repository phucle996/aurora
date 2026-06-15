// ============================================================================
// 📂 MODULE: controlplane/internal/iam/metrics/metrics.go
//            Điểm Đo Lường Trung Tâm Của Module IAM (OTel Metrics)
// ============================================================================
//
// 📊 METRICS GRAPH TREE:
//   aurora_controlplane
//   └── iam
//       ├── service_calls_total  [Counter]   — Đếm số lần gọi service IAM
//       └── downstream_duration_seconds [Histogram] — Đo latency downstream
//
// 🔌 ĐIỂM KẾT NỐI:
//   • Sử dụng otel.Meter() global, lazy init qua sync.Once.
//   • Không cần callback RegisterModuleMetrics như Prometheus cũ.
//
// 🎯 NGUYÊN TẮC:
//   • 1 Counter + 1 Histogram cho toàn module IAM (Rule of Two).
//   • Tất cả hàm Observe safe khi instrument nil (unit test / chưa init).
// ============================================================================

package iamMetrics

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ──────────────────────────────────────────────────────────────────────────────
// OTel INSTRUMENT VARIABLES (lazy init qua sync.Once)
// ──────────────────────────────────────────────────────────────────────────────

var (
	// serviceCallsCounter đếm tổng số lần gọi service layer IAM.
	serviceCallsCounter metric.Int64Counter

	// downstreamDuration đo latency (giây) các tác vụ downstream của module IAM.
	downstreamDuration metric.Float64Histogram

	// initOnce đảm bảo instruments chỉ được tạo một lần duy nhất.
	initOnce sync.Once
)

// ensureInit khởi tạo OTel instruments một cách an toàn đa luồng.
// Sử dụng Global MeterProvider (đã được app.go bootstrap thiết lập trước).
func ensureInit() {
	initOnce.Do(func() {
		meter := otel.Meter("aurora-controlplane.iam")

		// Counter: đếm tổng số lần gọi service IAM theo workflow và result
		serviceCallsCounter, _ = meter.Int64Counter(
			"aurora_controlplane_iam_service_calls_total",
			metric.WithDescription("Total IAM service calls, partitioned by workflow and result outcome."),
		)

		// Histogram: đo latency downstream IAM (DB, Redis, Crypto)
		downstreamDuration, _ = meter.Float64Histogram(
			"aurora_controlplane_iam_downstream_duration_seconds",
			metric.WithDescription("Latency in seconds of IAM downstream calls."),
		)
	})
}

// ──────────────────────────────────────────────────────────────────────────────
// PUBLIC API — 2 hàm generic cho mọi callsite trong module IAM
// ──────────────────────────────────────────────────────────────────────────────

// ServiceCall ghi nhận một lần gọi service IAM.
// Callsite tự truyền đầy đủ label values.
func ServiceCall(workflow, result string) {
	ensureInit()
	if serviceCallsCounter != nil {
		serviceCallsCounter.Add(context.Background(), 1,
			metric.WithAttributes(
				attribute.String("workflow", workflow),
				attribute.String("result", result),
			),
		)
	}
}

// Downstream ghi nhận latency của một tác vụ downstream IAM.
// Callsite tự truyền đầy đủ label values.
func Downstream(kind, workflow, destination, result string, duration time.Duration, err error) {
	ensureInit()
	if downstreamDuration != nil {
		status := "ok"
		if err != nil {
			status = "error"
		}
		downstreamDuration.Record(context.Background(), duration.Seconds(),
			metric.WithAttributes(
				attribute.String("kind", kind),
				attribute.String("workflow", workflow),
				attribute.String("destination", destination),
				attribute.String("result", result),
				attribute.String("status", status),
			),
		)
	}
}
