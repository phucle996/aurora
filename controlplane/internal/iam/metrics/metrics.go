// ======================================================================================================
// 📂 MODULE: controlplane/internal/iam/metrics/metrics.go
//            Điểm Đo Lường Trung Tâm Của Module IAM (Prometheus Telemetry)
// ======================================================================================================
//
// 📊 METRICS GRAPH TREE — VỊ TRÍ TRONG CÂY ĐO LƯỜNG CỦA CONTROLPLANE:
//
//   aurora_controlplane                          (namespace – toàn hệ thống)
//   └── iam                                      (subsystem – module định danh & xác thực)
//       ├── service_calls_total  [CounterVec]    ← FILE NÀY QUẢN LÝ
//       │     Đếm tổng số lần gọi service layer của IAM, phân theo luồng, kết quả và cache path.
//       │     Callsite tự truyền giá trị label — metric này là generic cho mọi IAM flow.
//       │     Labels:
//       │       flow       = bất kỳ tên luồng IAM nào ("register", "login", "refresh_token", ...)
//       │       result     = outcome code từ taxonomy ("success", "already_exists", ...)
//       │       cache_path = trạng thái presence cache ("cache_miss", "n/a", ...)
//       │
//       └── downstream_duration_seconds  [HistogramVec]    ← FILE NÀY QUẢN LÝ
//             Đo latency (giây) của các tác vụ downstream IAM gọi xuống.
//             Callsite tự truyền giá trị label — metric này là generic cho mọi downstream call.
//             Labels:
//               kind     = loại downstream ("db", "redis", "crypto")
//               workflow = tên luồng IAM ("register", "login", "admin_rotation", ...)
//               result   = outcome code tại bước đó ("success", "delivery_fail", ...)
//               status   = "ok" | "error"
//
// ❌ PHẠM VI KHÔNG BAO GỒM (đo đạc ở nơi khác):
//   • HTTP-level metrics (request_total, request_duration_seconds, in_flight):
//     → internal/http/middleware/observability.go
//   • DB-level pgx tracing tổng hợp:
//     → internal/observability/pgx_tracer.go
//   • Redis hook tracing tổng hợp:
//     → internal/observability/redis_hook.go
//   • Global dependency histogram (dependency_duration_seconds):
//     → internal/observability/prometheus.go
//
// 🔌 ĐIỂM KẾT NỐI VÀO HỆ THỐNG:
//   • init() → observability.RegisterModuleMetrics(Register)
//   • Register() được observability gọi khi khởi tạo Prometheus Registry.
//   • Đăng ký đúng 1 lần (sync.Once), an toàn với môi trường concurrent.
//
// 🎯 NGUYÊN TẮC THIẾT KẾ:
//   • 1 CounterVec + 1 HistogramVec cho toàn bộ module IAM — không tạo thêm metric mới.
//   • API công khai gồm đúng 2 hàm generic: ObserveServiceCall & ObserveDownstream.
//   • Callsite (service layer) tự truyền đầy đủ label value — metric không hard-code flow name.
//   • Tất cả hàm Observe đều safe khi metric nil (chưa khởi tạo / unit test).
// ======================================================================================================

package iamMetrics

import (
	"sync"
	"time"

	"controlplane/internal/observability"

	"github.com/prometheus/client_golang/prometheus"
)

// ──────────────────────────────────────────────────────────────────────────────
// METRIC VARIABLES
// ──────────────────────────────────────────────────────────────────────────────

// serviceCallsCounter đếm tổng số lần gọi service layer IAM.
// FQN: aurora_controlplane_iam_service_calls_total
var serviceCallsCounter *prometheus.CounterVec

// downstreamDuration đo latency (giây) các tác vụ downstream của module IAM.
// FQN: aurora_controlplane_iam_downstream_duration_seconds
var downstreamDuration *prometheus.HistogramVec

// ──────────────────────────────────────────────────────────────────────────────
// MODULE REGISTRATION (self-register vào Prometheus Registry qua init)
// ──────────────────────────────────────────────────────────────────────────────

var registerOnce sync.Once

// Register đăng ký toàn bộ IAM metrics vào Prometheus Registry.
// Namespace đã được chuẩn hóa bởi observability.NormalizeNamespace trước khi được truyền vào đây.
// Nếu namespace không hợp lệ, lỗi sẽ được trả lên observability layer để xử lý theo policies engine.
func Register(registry *prometheus.Registry, namespace string) error {
	var registerErr error
	registerOnce.Do(func() {
		registerErr = registerIAMMetrics(registry, namespace)
	})
	return registerErr
}

func init() {
	// Tự đăng ký module vào callback chain của observability layer.
	// observability.RegisterModuleMetrics gọi Register() khi Prometheus Registry được khởi tạo.
	observability.RegisterModuleMetrics(Register)
}

// registerIAMMetrics khởi tạo và đăng ký 2 metric dùng chung cho toàn module IAM.
func registerIAMMetrics(registry *prometheus.Registry, namespace string) error {
	// CounterVec: đếm tổng số lần gọi service IAM, phân loại đa chiều theo label.
	serviceCallsCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "iam",
		Name:      "service_calls_total",
		Help:      "Total IAM service calls, partitioned by flow, result outcome and cache path.",
	}, []string{"flow", "result", "cache_path"})

	// HistogramVec: đo latency downstream của IAM với buckets phù hợp cho cả 3 loại tác vụ:
	//   - Argon2id hash (~100–500ms, CPU-bound)
	//   - Postgres write (~1–20ms, I/O-bound)
	//   - Redis check   (~0.1–5ms, network-bound)
	downstreamDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "iam",
		Name:      "downstream_duration_seconds",
		Help:      "Latency in seconds of IAM downstream calls (db, redis, crypto), partitioned by kind, workflow, destination, result and status.",
		Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5},
	}, []string{"kind", "workflow", "destination", "result", "status"})

	for _, c := range []prometheus.Collector{serviceCallsCounter, downstreamDuration} {
		if err := registry.Register(c); err != nil {
			return err
		}
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// PUBLIC API — 2 hàm generic cho mọi callsite trong module IAM
// ──────────────────────────────────────────────────────────────────────────────

// ServiceCall ghi nhận một lần gọi service IAM vào serviceCallsCounter.
// Callsite tự truyền đầy đủ 3 label, giữ tính generic cho mọi IAM flow.
func ServiceCall(flow, result, cachePath string) {
	if serviceCallsCounter != nil {
		serviceCallsCounter.WithLabelValues(flow, result, cachePath).Inc()
	}
}

// Downstream ghi nhận latency của một tác vụ downstream IAM vào downstreamDuration.
// Callsite tự truyền đầy đủ các label để giữ tính generic.
func Downstream(kind, workflow, destination, result string, duration time.Duration, err error) {
	if downstreamDuration != nil {
		status := "ok"
		if err != nil {
			status = "error"
		}
		downstreamDuration.WithLabelValues(kind, workflow, destination, result, status).Observe(duration.Seconds())
	}
}
