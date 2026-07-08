package iamMetrics

import (
	"context"
	"sync"
	"time"

	"controlplane/pkg/constant"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ──────────────────────────────────────────────────────────────────────────────
// STANDARDIZED DOWNSTREAM KINDS (Các loại Downstream được chuẩn hóa theo God View)
// ──────────────────────────────────────────────────────────────────────────────

const (
	KindRepo              = "repo"
	KindCacheEngineL1     = "cache-engine-l1"
	KindCacheEngineL2     = "cache-engine-l2"
	KindCacheEngineFanout = "cache-engine-fanout"
	KindCacheEngineExcute = "cache-engine-execute"
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
	// serviceCallsCounter đếm tổng số lần gọi service layer IAM.
	serviceCallsCounter metric.Int64Counter

	// downstreamDuration đo latency (giây) các tác vụ downstream của module IAM.
	downstreamDuration metric.Float64Histogram

	// initOnce đảm bảo instruments chỉ được tạo một lần duy nhất.
	initOnce sync.Once
)

// Init khởi tạo các OTel instruments một cách tường minh từ observability/otel.
// Giúp kiểm soát thứ tự bootup và loại bỏ hoàn toàn lock/sync ở hot path.
func Init(meterProvider metric.MeterProvider) {
	initOnce.Do(func() {
		meter := meterProvider.Meter("aurora-controlplane.iam")

		// Counter: đếm tổng số lần gọi service IAM theo op (operation) và outcome
		// Sử dụng nhãn 'op' để đồng bộ với context và 'outcome' thay cho 'result' cũ
		serviceCallsCounter, _ = meter.Int64Counter(
			"aurora_controlplane_iam_service_calls_total",
			metric.WithDescription("Total IAM service calls, partitioned by op and outcome."),
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
// Lấy thông tin workflow (op) trực tiếp từ Go context thay vì truyền cứng.
func ServiceCall(ctx context.Context, outcome string) {
	// Chỉ kiểm tra con trỏ khác nil thay vì gọi ensureInit() liên tục
	if serviceCallsCounter != nil {
		// Trích xuất tên operation từ context
		op := constant.GetOperation(ctx)

		// Ghi nhận counter, truyền ctx để OTel có thể đính kèm Trace ID dưới dạng Exemplar.
		// Nhãn được đổi tên từ 'workflow' sang 'op' và 'result' sang 'outcome' để tăng tính nhất quán.
		serviceCallsCounter.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("op", op),
				attribute.String("outcome", outcome),
			),
		)
	}
}

// Downstream ghi nhận latency của một tác vụ downstream IAM.
// Lấy thông tin workflow (op) trực tiếp từ Go context thay vì truyền cứng.
func Downstream(ctx context.Context, kind, destination, outcome string, duration time.Duration, err error) {
	// Chỉ kiểm tra con trỏ khác nil thay vì gọi ensureInit() liên tục
	if downstreamDuration != nil {
		status := "ok"
		if err != nil {
			status = "error"
		}
		// Trích xuất tên operation từ context
		op := constant.GetOperation(ctx)

		// Ghi nhận latency histogram, truyền ctx để hỗ trợ Trace Exemplar.
		// Áp dụng nhãn 'op' cho operation cha và 'outcome' cho kết quả của downstream call.
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
