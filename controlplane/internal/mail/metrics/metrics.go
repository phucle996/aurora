// ============================================================================
// 📂 MODULE: controlplane/internal/mail/metrics/metrics.go
//            Đo Lường Chỉ Số Vận Hành Dịch Vụ Mail (OTel Metrics)
//            Tham chiếu: god_view/mail/create_endpoint_god_view_workflow.md
// ============================================================================

package mailMetrics

import (
	"context"
	"sync"
	"time"

	"controlplane/pkg/context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ──────────────────────────────────────────────────────────────────────────────
// STANDARDIZED DOWNSTREAM KINDS (Các loại Downstream được chuẩn hóa theo God View)
// ──────────────────────────────────────────────────────────────────────────────

const (
	KindRepo          = "repo"
	KindCacheEngineL2 = "cache-engine-l2"
)

// ──────────────────────────────────────────────────────────────────────────────
// STANDARDIZED SERVICE CALL OUTCOMES (Các loại Outcome được chuẩn hóa theo God View)
// ──────────────────────────────────────────────────────────────────────────────

const (
	OutcomeSuccess            = "success"
	OutcomeFailure            = "failure"
	OutcomeFailureUnknown     = "failure_unknown"
	OutcomePreConditionFailed = "precondition_failed"
	OutcomeInvalidCredential  = "invalid_credential"
	OutcomeLockBusy           = "lock_busy"
)

// ──────────────────────────────────────────────────────────────────────────────
// OTel INSTRUMENT VARIABLES (lazy init qua sync.Once)
// ──────────────────────────────────────────────────────────────────────────────

var (
	initOnce sync.Once

	// serviceCallsCounter đếm tổng số lần gọi service layer Mail.
	serviceCallsCounter metric.Int64Counter

	// downstreamDuration đo latency (giây) các tác vụ downstream của module Mail.
	downstreamDuration metric.Float64Histogram
)

// Init khởi tạo các OTel instruments một cách tường minh từ observability/otel.
// Giúp kiểm soát thứ tự bootup và loại bỏ hoàn toàn lock/sync ở hot path.
func Init(meterProvider metric.MeterProvider) {
	initOnce.Do(func() {
		meter := meterProvider.Meter("aurora-controlplane.mail")

		// Service Calls: Đếm tổng số cuộc gọi service Mail, phân loại theo op và outcome
		serviceCallsCounter, _ = meter.Int64Counter(
			"aurora_controlplane_mail_service_calls_total",
			metric.WithDescription("Total Mail service calls, partitioned by op and outcome."),
		)

		// Downstream Duration: Đo latency các tác vụ downstream Mail (Postgres, Redis)
		downstreamDuration, _ = meter.Float64Histogram(
			"aurora_controlplane_mail_downstream_duration_seconds",
			metric.WithDescription("Latency in seconds of Mail downstream calls."),
		)
	})
}

// ──────────────────────────────────────────────────────────────────────────────
// PUBLIC API — 2 hàm generic cho mọi callsite trong module Mail
// ──────────────────────────────────────────────────────────────────────────────

// ServiceCall ghi nhận một lần gọi service Mail.
// Lấy thông tin operation (op) trực tiếp từ Go context thay vì truyền cứng.
func ServiceCall(ctx context.Context, outcome string) {
	// Kiểm tra con trỏ khác nil thay vì gọi sync/lock trên hot path
	if serviceCallsCounter != nil {
		op := pkgcontext.GetOperation(ctx)
		serviceCallsCounter.Add(ctx, 1, metric.WithAttributes(
			attribute.String("op", op),
			attribute.String("outcome", outcome),
			attribute.String("module", "mail"), // Nhãn module để phân biệt dễ dàng trên Prometheus
		))
	}
}

// Downstream ghi nhận latency của một tác vụ downstream Mail.
// Lấy thông tin operation (op) trực tiếp từ Go context thay vì truyền cứng.
func Downstream(ctx context.Context, kind, destination, outcome string, duration time.Duration, err error) {
	// Kiểm tra con trỏ khác nil thay vì gọi sync/lock trên hot path
	if downstreamDuration != nil {
		status := "ok"
		if err != nil {
			status = "error"
		}
		op := pkgcontext.GetOperation(ctx)
		downstreamDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(
			attribute.String("kind", kind),
			attribute.String("op", op),
			attribute.String("destination", destination),
			attribute.String("outcome", outcome),
			attribute.String("status", status),
			attribute.String("module", "mail"), // Nhãn module để phân biệt dễ dàng trên Prometheus
		))
	}
}
