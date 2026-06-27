// ============================================================================
// 📂 FILE: internal/observability/metrics.go - Hạ Tầng Telemetry OTel Metrics
// ============================================================================
//
// 📌 VAI TRÒ CHỦ ĐẠO (PRIMARY ROLE):
//   - Thay thế hoàn toàn prometheus.go cũ, chuyển sang native OpenTelemetry Metrics SDK.
//   - Tất cả metrics được push qua OTLP gRPC đến OTel Collector thay vì pull-based Prometheus.
//   - Loại bỏ hoàn toàn dependency prometheus/client_golang.
//
// 🎯 SOURCE OF TRUTH (SoT):
//     thông qua dynamic active policy hook.
//
// 🔒 RANH GIỚI BẢO MẬT & NGHIỆP VỤ (SECURITY & OPERATIONAL BOUNDARIES):
//   - Zero-Inbound: Không còn HTTP endpoint /metrics. Tất cả dữ liệu push ra ngoài qua OTLP.
//   - Thread-safety: Sử dụng atomic.Pointer cho hoán đổi nóng cấu hình chính sách.
//   - Panic-safe: Tất cả hàm Observe kiểm tra nil trước khi ghi nhận metrics.
//
// ============================================================================

package observability

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ============================================================================
// 📦 BIẾN TOÀN CỤC & CACHE (GLOBAL VARIABLES)
// ============================================================================

var (
	// currentMetrics lưu trữ thực thể Metrics đang hoạt động, truy cập an toàn luồng qua atomic.
	currentMetrics atomic.Pointer[Metrics]

	// localHostname lưu trữ tên Pod/Container một lần duy nhất để tránh system call lặp lại.
	localHostname string

	// timeSyncStates dùng cho one-hot encoding trạng thái đồng bộ thời gian, cố định tránh heap alloc.
	timeSyncStates = []string{"ok", "warning", "critical", "unknown"}
)

func init() {
	// [KHỞI TẠO TĨNH HOSTNAME]: Lấy tên Pod/Container một lần duy nhất khi khởi động.
	h, err := os.Hostname()
	if err != nil {
		h = "unknown_pod"
	}
	localHostname = h
}

// ============================================================================
// 📊 CẤU TRÚC DỮ LIỆU TRUNG TÂM (CENTRAL METRICS STRUCT)
// ============================================================================

// Metrics quản lý toàn bộ OTel instruments đo lường trung tâm của Controlplane.
// Thay thế hoàn toàn struct Prometheus cũ dùng prometheus/client_golang.
type Metrics struct {
	// OTel Instruments - thay thế prometheus.CounterVec / HistogramVec / Gauge
	requestTotal    metric.Int64Counter       // Tổng HTTP requests (method, route, status)
	requestDuration metric.Float64Histogram   // Latency HTTP (method, route, status)
	inFlight        metric.Int64UpDownCounter // Số request đang xử lý đồng thời
	dependencyDur   metric.Float64Histogram   // Latency downstream (kind, operation, status)
	timeDriftGauge  metric.Float64Gauge       // Độ lệch thời gian hệ thống (seconds)
	timeSyncGauge   metric.Float64Gauge       // Trạng thái đồng bộ thời gian (one-hot)
}

// ============================================================================
// 📦 HÀM KHỞI TẠO & QUẢN LÝ VÒNG ĐỜI (LIFECYCLE MANAGEMENT)
// ============================================================================

// LocalHostname trả về định danh Hostname/Pod Name hiện tại đã được cache trong RAM.
func LocalHostname() string {
	return localHostname
}

// NullMetrics khởi tạo thực thể rỗng (Null Object Pattern) cho cơ chế Fail-Open.
// Khi hệ thống giám sát gặp sự cố, middleware và nghiệp vụ vẫn hoạt động trơn tru
// do tất cả hàm Observe đều kiểm tra nil trước khi ghi nhận.
func NullMetrics() *Metrics {
	return &Metrics{}
}

// Enabled trả về true nếu Metrics được khởi tạo thành công và đang hoạt động.
func (m *Metrics) Enabled() bool {
	return m != nil && m.requestTotal != nil
}

// normalizeNamespace chuẩn hóa namespace thành chuỗi hợp lệ cho OpenMetrics naming.
// Chuyển lowercase, thay dấu gạch ngang/khoảng trắng thành gạch dưới, lọc ký tự đặc biệt.
func normalizeNamespace(ns string) string {
	ns = strings.ToLower(strings.TrimSpace(ns))
	ns = strings.ReplaceAll(ns, "-", "_")
	ns = strings.ReplaceAll(ns, " ", "_")

	var clean []rune
	for _, r := range ns {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			clean = append(clean, r)
		}
	}
	ns = string(clean)
	if ns == "" {
		ns = "aurora"
	}
	return ns
}

// InitMetrics khởi tạo toàn bộ OTel instruments đo lường trung tâm cho Controlplane.
// Lấy Meter từ Global MeterProvider đã được InitOTel thiết lập trước đó trong bootstrap.
func InitMetrics(namespace string) (*Metrics, error) {
	namespace = normalizeNamespace(namespace)

	// Lấy Meter từ Global MeterProvider (thiết lập bởi InitOTel trong app.go bootstrap)
	meter := otel.Meter("aurora-controlplane")

	// [1] Counter: Tổng số HTTP requests phân loại theo method/route/status
	requestTotal, err := meter.Int64Counter(
		namespace+"_http_requests_total",
		metric.WithDescription("Total number of HTTP requests processed by route/method/status."),
	)
	if err != nil {
		return nil, err
	}

	// [2] Histogram: Latency xử lý HTTP requests
	requestDuration, err := meter.Float64Histogram(
		namespace+"_http_request_duration_seconds",
		metric.WithDescription("HTTP request latency by route/method/status."),
	)
	if err != nil {
		return nil, err
	}

	// [3] UpDownCounter: Số lượng requests đang xử lý đồng thời (In-Flight)
	inFlight, err := meter.Int64UpDownCounter(
		namespace+"_http_in_flight_requests",
		metric.WithDescription("Current number of in-flight HTTP requests."),
	)
	if err != nil {
		return nil, err
	}

	// [4] Histogram: Latency của DB/Redis/Crypto và các dependency bên ngoài
	dependencyDur, err := meter.Float64Histogram(
		namespace+"_dependency_duration_seconds",
		metric.WithDescription("Dependency latency by kind/operation/status."),
	)
	if err != nil {
		return nil, err
	}

	// [5] Gauge: Độ lệch thời gian hệ thống (giây) từ nguồn Chrony
	timeDriftGauge, err := meter.Float64Gauge(
		namespace+"_system_time_drift_seconds",
		metric.WithDescription("Absolute system time drift in seconds from chrony source."),
	)
	if err != nil {
		return nil, err
	}

	// [6] Gauge: Trạng thái đồng bộ thời gian dạng one-hot (ok/warning/critical/unknown)
	timeSyncGauge, err := meter.Float64Gauge(
		namespace+"_system_time_sync_state",
		metric.WithDescription("Time sync state as one-hot gauge labels."),
	)
	if err != nil {
		return nil, err
	}

	m := &Metrics{
		requestTotal:    requestTotal,
		requestDuration: requestDuration,
		inFlight:        inFlight,
		dependencyDur:   dependencyDur,
		timeDriftGauge:  timeDriftGauge,
		timeSyncGauge:   timeSyncGauge,
	}

	// Lưu vào biến toàn cục an toàn luồng
	currentMetrics.Store(m)
	return m, nil
}

// ============================================================================
// 📊 HÀM GHI NHẬN ĐO LƯỜNG (OBSERVE FUNCTIONS)
// ============================================================================

// CurrentMetrics trả về thực thể Metrics đang hoạt động toàn cục.
func CurrentMetrics() *Metrics {
	return currentMetrics.Load()
}

// ClearCurrentMetrics dọn dẹp và ngắt kết nối thực thể Metrics toàn cục.
func ClearCurrentMetrics() {
	currentMetrics.Store(nil)
}

// IncInFlight tăng số lượng in-flight requests đang xử lý lên 1.
func (m *Metrics) IncInFlight() {
	if m != nil && m.inFlight != nil {
		m.inFlight.Add(context.Background(), 1)
	}
}

// DecInFlight giảm số lượng in-flight requests đang xử lý đi 1.
func (m *Metrics) DecInFlight() {
	if m != nil && m.inFlight != nil {
		m.inFlight.Add(context.Background(), -1)
	}
}

// ObserveRequest ghi nhận số liệu thống kê HTTP API request.
// Chuẩn hóa và gán nhãn mặc định an toàn cho các tham số trống.
func (m *Metrics) ObserveRequest(method, route, status string, duration time.Duration) {
	if m == nil || m.requestTotal == nil || m.requestDuration == nil {
		return
	}
	// Chuẩn hóa chuỗi input và gán giá trị mặc định an toàn
	method = strings.TrimSpace(method)
	route = strings.TrimSpace(route)
	status = strings.TrimSpace(status)
	if route == "" {
		route = "/"
	}
	if method == "" {
		method = "UNKNOWN"
	}
	if status == "" {
		status = "0"
	}

	ctx := context.Background()
	attrs := metric.WithAttributes(
		attribute.String("method", method),
		attribute.String("route", route),
		attribute.String("status", status),
	)
	m.requestTotal.Add(ctx, 1, attrs)
	m.requestDuration.Record(ctx, duration.Seconds(), attrs)
}

// ObserveDependency ghi nhận latency và phân loại kết quả (ok/error) của dependency bên ngoài.
// API chung cho mọi module gọi xuống: db, redis, crypto, và bất kỳ kind nào.
func (m *Metrics) ObserveDependency(kind, operation string, duration time.Duration, err error) {
	if m == nil || m.dependencyDur == nil {
		return
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = "unknown"
	}
	operation = strings.TrimSpace(operation)
	if operation == "" {
		operation = "unknown"
	}
	status := "ok"
	if err != nil {
		status = "error"
	}
	m.dependencyDur.Record(context.Background(), duration.Seconds(),
		metric.WithAttributes(
			attribute.String("kind", kind),
			attribute.String("operation", operation),
			attribute.String("status", status),
		),
	)
}

// ObserveTimeDrift ghi nhận độ lệch thời gian hệ thống và cập nhật trạng thái one-hot tương ứng.
func (m *Metrics) ObserveTimeDrift(seconds float64, state string) {
	if m == nil || m.timeDriftGauge == nil || m.timeSyncGauge == nil {
		return
	}
	ctx := context.Background()
	m.timeDriftGauge.Record(ctx, seconds)

	// Thiết lập trạng thái one-hot: trạng thái khớp nhận 1.0, còn lại nhận 0.0
	for _, s := range timeSyncStates {
		v := 0.0
		if s == state {
			v = 1.0
		}
		m.timeSyncGauge.Record(ctx, v, metric.WithAttributes(
			attribute.String("state", s),
		))
	}
}

// ObserveAdminAction ghi nhận hành động quản trị (Admin/Audit actions) trong hệ thống.
func (m *Metrics) ObserveAdminAction(resource, action, result string) {
	if m == nil || m.requestTotal == nil {
		return
	}
	m.requestTotal.Add(context.Background(), 1,
		metric.WithAttributes(
			attribute.String("method", "admin"),
			attribute.String("route", resource+"."+action),
			attribute.String("status", result),
		),
	)
}
