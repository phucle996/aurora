// ============================================================================
// 📂 FILE: internal/observability/prometheus.go - Hạ Tầng Telemetry Prometheus
// ============================================================================
//
// 📌 VAI TRÒ CHỦ ĐẠO (PRIMARY ROLE):
//   - Chịu trách nhiệm khởi tạo, quản lý và lưu giữ Registry đo lường (Telemetry metrics)
//     của toàn bộ tiến trình Controlplane.
//   - Hỗ trợ Dynamic Query Client bằng `atomic.Pointer` để hoán đổi nóng (Hot-swap) các
//     tham số kết nối từ Policy Engine mà không gây downtime hoặc Address conflict.
//   - Triển khai mô hình Null Object Pattern (`NullPrometheus`) làm cơ chế Fail-Open dự phòng
//     khi xảy ra gián đoạn kết nối hoặc cụm giám sát không khả dụng tạm thời trong môi trường HA/Cloud-Native.
//     (Lưu ý: Nếu cấu hình sai địa chỉ hoặc thông số hoàn toàn không hợp lệ ngay từ đầu, đó là lỗi của người vận hành
//     viết chính sách. Trình biên dịch chính sách sẽ từ chối ngay lập tức để kích hoạt Last-Known-Good bảo vệ hệ thống,
//     chứ không lạm dụng cơ chế Fail-Open này).
//   - Đọc `os.Hostname()` tự động để định danh Pod Name phục vụ gán nhãn hạ tầng.
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Trực tiếp tiêu thụ cấu hình biên dịch từ `policyengine/policies/prometheus` thông qua
//     dynamic active policy hook.
//
// 🔒 RANH GIỚI BẢO MẬT & NGHIỆP VỤ (SECURITY & OPERATIONAL BOUNDARIES):
//   - Ranh giới Hot-swap: Cổng TCP cào metrics (`RoutePath`) được cố định ở lớp mạng (Static Port Binding)
//     để tránh address-in-use runtime conflict, chỉ hoán đổi động HTTP path, Timeout, và URL kết nối.
//   - Ranh giới Thread-safety: Tất cả các thao tác cập nhật cấu hình hoặc ghi nhận metric
//     đều bắt buộc đạt độ an toàn luồng tuyệt đối (Lock-free bằng atomic ops), tránh nghẽn luồng.
//   - Ranh giới Write-Only: Phân hệ Rate Limiter và Security chỉ ghi metrics vào Registry này,
//     hoàn toàn không thực hiện truy vấn ngược để tránh vòng lặp phụ thuộc (Circular Dependency).
//
// 💡 LƯU Ý QUAN TRỌNG (HA & CLOUD-NATIVE NOTES):
//   - Việc đọc `os.Hostname()` chỉ được thực thi một lần duy nhất tại hàm `init` để tối ưu
//     hiệu năng, tránh chi phí system call lặp đi lặp lại trong môi trường container.
//   - Namespace được tự động chuẩn hóa qua hàm `NormalizeNamespace` để loại bỏ các ký tự đặc biệt,
//     giúp metrics tuân thủ nghiêm ngặt chuẩn OpenMetrics / Prometheus.
//
// ============================================================================

package observability

import (
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	promPolicy "controlplane/internal/policyengine/policies/prometheus"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Global Variables & Cache
var (
	// currentPrometheus là thực thể lưu trữ dynamic pointer của phân hệ Prometheus đang hoạt động.
	currentPrometheus atomic.Pointer[Prometheus]

	// localHostname lưu trữ tên Pod Name/Hostname hiện tại của hệ thống.
	localHostname string

	// timeSyncStates định nghĩa mảng trạng thái cố định để tránh heap allocation lúc runtime loop.
	timeSyncStates = []string{"ok", "warning", "critical", "unknown"}
)

func init() {
	// [KHỞI TẠO TĨNH HOSTNAME]: Tự động lấy tên Pod/Container ngay khi khởi động.
	// Giá trị này được lưu trữ cố định trong RAM đến hết toàn bộ vòng đời của Pod/Container,
	// loại bỏ hoàn toàn việc thực hiện system call lặp đi lặp lại tại runtime để tối ưu hiệu năng tuyệt đối.
	h, err := os.Hostname()
	if err != nil {
		h = "unknown_pod"
	}
	localHostname = h
}

// QueryClientConfig lưu trữ động cấu hình truy vấn của Prometheus
type QueryClientConfig struct {
	Enabled      bool          // Trạng thái bật/tắt của Prometheus Query Client
	BaseURL      string        // Địa chỉ máy chủ Prometheus Server (Ví dụ: http://prometheus:9090)
	QueryTimeout time.Duration // Thời gian chờ tối đa cho các truy vấn Prometheus
	DefaultStep  time.Duration // Khoảng thời gian (Step) mặc định cho các câu lệnh Range Query
}

// Prometheus đại diện cho đối tượng quản lý Registry đo lường tập trung
type Prometheus struct {
	registry           *prometheus.Registry     // Registry chứa toàn bộ metrics được đăng ký của Controlplane
	requestTotal       *prometheus.CounterVec   // Counter đo lường tổng lượng HTTP requests phân loại theo (method, route, status)
	requestDuration    *prometheus.HistogramVec // Histogram thu thập latency HTTP requests
	inFlight           prometheus.Gauge         // Gauge đo lường số lượng requests đang được xử lý đồng thời
	dependencyDur      *prometheus.HistogramVec // Histogram đo lường độ trễ của DB, Redis và các bên thứ ba
	timeDriftGauge     prometheus.Gauge         // Gauge giám sát độ lệch thời gian (time drift) của hệ thống
	timeSyncStateGauge *prometheus.GaugeVec     // Gauge one-hot biểu thị trạng thái đồng bộ thời gian (ok, warning, critical, unknown)

	// Các con trỏ động an toàn luồng phục vụ cơ chế hoán đổi nóng (Hot-swap) cấu hình chính sách
	policyConfig atomic.Pointer[promPolicy.CompiledPolicy]
	queryConfig  atomic.Pointer[QueryClientConfig]
}

// LocalHostname trả về định danh Hostname/Pod Name hiện tại đã được cache trong RAM.
func LocalHostname() string {
	return localHostname
}

// NullPrometheus khởi tạo một thực thể rỗng theo mô hình thiết kế Null Object Pattern.
//
// 🎯 CONTRACT & MECHANISM:
//   - Trả về một đối tượng `Prometheus` hợp lệ nhưng không chứa bất kỳ Registry hay Vector thực tế nào.
//   - Phục vụ hoàn hảo cho cơ chế Fail-Open: Khi hệ thống giám sát Prometheus gặp sự cố gián đoạn tạm thời,
//     các middleware và dịch vụ nghiệp vụ vẫn hoạt động trơn tru mà không bị crash hoặc panic do con trỏ null.
//     (Chú ý: Cơ chế này dành cho sự cố gián đoạn tạm thời của cụm giám sát. Nếu không có cấu hình hợp lệ do lỗi
//     người vận hành viết sai chính sách, lỗi đó sẽ được ngăn chặn triệt để từ lớp compiler trước đó).
func NullPrometheus() *Prometheus {
	p := &Prometheus{}
	p.policyConfig.Store(&promPolicy.CompiledPolicy{Enabled: false})
	p.queryConfig.Store(&QueryClientConfig{Enabled: false})
	return p
}

// Danh sách các hàm đăng ký metric động từ các mô-đun nghiệp vụ độc lập (Ví dụ: Rate Limiter, Auth).
var (
	moduleMetricRegistrars []func(registry *prometheus.Registry, namespace string) error
)

// RegisterModuleMetrics cho phép các mô-đun bên ngoài đăng ký hàm khởi tạo metric của riêng mình
// vào danh sách đăng ký tập trung trước khi Registry được khởi tạo.
func RegisterModuleMetrics(registrar func(registry *prometheus.Registry, namespace string) error) {
	if registrar == nil {
		return
	}
	moduleMetricRegistrars = append(moduleMetricRegistrars, registrar)
}

// registerModuleMetrics duyệt qua danh sách moduleMetricRegistrars để thực thi đăng ký metrics cho từng mô-đun.
func registerModuleMetrics(registry *prometheus.Registry, namespace string) error {
	for _, registrar := range moduleMetricRegistrars {
		if err := registrar(registry, namespace); err != nil {
			return err
		}
	}
	return nil
}

// NormalizeNamespace thực hiện chuẩn hóa chuỗi namespace đầu vào.
//
// 🎯 CHUẨN HÓA CLOUD-NATIVE:
//   - Chuyển toàn bộ ký tự sang chữ thường (lowercase).
//   - Đổi tất cả dấu gạch ngang `-` và khoảng trắng thành dấu gạch dưới `_`.
//   - Loại bỏ tất cả các ký tự không hợp lệ (chỉ giữ lại chữ cái `a-z`, chữ số `0-9` và dấu gạch dưới `_`).
//   - Nếu chuỗi rỗng sau chuẩn hóa, trả về mặc định `"aurora"`.
func NormalizeNamespace(ns string) string {
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

// InitPrometheus thực hiện khởi tạo Registry mới và thiết lập toàn bộ tập hợp metrics chuẩn hóa.
//
// 🎯 LOGIC THỰC THI:
//  1. Thực hiện chuẩn hóa Namespace an toàn cho Prometheus.
//  2. Khởi tạo một Registry trống mới và đăng ký GoCollector cùng ProcessCollector của hệ thống.
//  3. Tạo các Vector đo lường tiêu chuẩn (Request, Latency, In-Flight, Dependency, Time Drift).
//  4. Đăng ký các Vector này vào Registry.
//  5. Gọi `registerModuleMetrics` để tích hợp metrics từ các mô-đun nghiệp vụ độc lập khác.
//  6. Lưu trữ đối tượng vào luồng an toàn toàn cục `currentPrometheus`.
func InitPrometheus(namespace string) (*Prometheus, error) {
	namespace = NormalizeNamespace(namespace)

	registry := prometheus.NewRegistry()
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	// Requests Counter Vector
	requestTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http",
		Name:      "requests_total",
		Help:      "Total number of HTTP requests processed by route/method/status.",
	}, []string{"method", "route", "status"})

	// Request Duration Histogram Vector
	requestDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "http",
		Name:      "request_duration_seconds",
		Help:      "HTTP request latency by route/method/status.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"method", "route", "status"})

	// Concurrent In-Flight Gauge
	inFlight := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: "http",
		Name:      "in_flight_requests",
		Help:      "Current number of in-flight HTTP requests.",
	})

	// Dependency Duration Histogram Vector
	dependencyDur := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "dependency",
		Name:      "duration_seconds",
		Help:      "Dependency latency by kind/operation/status.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"kind", "operation", "status"})

	// System Time Drift Gauge
	timeDriftGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: "system",
		Name:      "time_drift_seconds",
		Help:      "Absolute system time drift in seconds from chrony source.",
	})

	// Time Synchronization State Gauge Vector
	timeSyncStateGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: "system",
		Name:      "time_sync_state",
		Help:      "Time sync state as one-hot gauge labels.",
	}, []string{"state"})

	// Đăng ký toàn bộ các metrics vào Registry tập trung
	if err := registry.Register(requestTotal); err != nil {
		return nil, err
	}
	if err := registry.Register(requestDuration); err != nil {
		return nil, err
	}
	if err := registry.Register(inFlight); err != nil {
		return nil, err
	}
	if err := registry.Register(dependencyDur); err != nil {
		return nil, err
	}
	if err := registry.Register(timeDriftGauge); err != nil {
		return nil, err
	}
	if err := registry.Register(timeSyncStateGauge); err != nil {
		return nil, err
	}

	// Đăng ký metrics từ các mô-đun nghiệp vụ độc lập
	if err := registerModuleMetrics(registry, namespace); err != nil {
		return nil, err
	}

	prom := &Prometheus{
		registry:           registry,
		requestTotal:       requestTotal,
		requestDuration:    requestDuration,
		inFlight:           inFlight,
		dependencyDur:      dependencyDur,
		timeDriftGauge:     timeDriftGauge,
		timeSyncStateGauge: timeSyncStateGauge,
	}

	// Khởi tạo các con trỏ động rỗng (Dormant State) tránh lỗi truy cập bộ nhớ rỗng ban đầu
	prom.policyConfig.Store(&promPolicy.CompiledPolicy{Enabled: false})
	prom.queryConfig.Store(&QueryClientConfig{Enabled: false})

	currentPrometheus.Store(prom)
	return prom, nil
}

// UpdatePolicy thực hiện hoán đổi nóng chính sách Prometheus hiện thời một cách nguyên tử (Thread-safe).
//
// 🎯 CƠ CHẾ HOẠT ĐỘNG:
//   - Cập nhật con trỏ chính sách `policyConfig` bằng `Store` nguyên tử.
//   - Tự động phân tích và đồng bộ các cấu hình con trỏ `queryConfig` của Prometheus Query Client.
//   - Hoàn toàn an toàn khi gọi đồng thời từ nhiều Goroutines đồng bộ hóa chính sách.
func (p *Prometheus) UpdatePolicy(policy *promPolicy.CompiledPolicy) {
	if p == nil || policy == nil {
		return
	}
	p.policyConfig.Store(policy)

	// Đồng bộ hóa cấu hình truy vấn của Query Client tương ứng
	p.UpdateQueryConfig(&QueryClientConfig{
		Enabled:      policy.QueryClient.Enabled,
		BaseURL:      policy.QueryClient.BaseURL,
		QueryTimeout: policy.QueryClient.QueryTimeout,
		DefaultStep:  policy.QueryClient.DefaultStep,
	})
}

// GetPolicy lấy snapshot chính sách hoạt động hiện thời của phân hệ Prometheus.
//
// 🎯 CONTRACT & MECHANISM:
//   - Nếu instance hoặc con trỏ trống rỗng, trả về một CompiledPolicy rỗng (với Enabled: false)
//     để tránh lỗi truy cập địa chỉ RAM rỗng (Null pointer dereference).
//   - Lấy dữ liệu dạng lock-free bằng `Load()` nguyên tử.
func (p *Prometheus) GetPolicy() *promPolicy.CompiledPolicy {
	if p == nil {
		return &promPolicy.CompiledPolicy{Enabled: false}
	}
	val := p.policyConfig.Load()
	if val == nil {
		return &promPolicy.CompiledPolicy{Enabled: false}
	}
	return val
}

// UpdateQueryConfig cập nhật nguyên tử (atomic) cấu hình kết nối của Prometheus Query Client.
func (p *Prometheus) UpdateQueryConfig(cfg *QueryClientConfig) {
	if p == nil || cfg == nil {
		return
	}
	p.queryConfig.Store(cfg)
}

// GetQueryConfig trả về snapshot cấu hình kết nối hiện tại của Prometheus Query Client.
func (p *Prometheus) GetQueryConfig() *QueryClientConfig {
	if p == nil {
		return &QueryClientConfig{Enabled: false}
	}
	val := p.queryConfig.Load()
	if val == nil {
		return &QueryClientConfig{Enabled: false}
	}
	return val
}

// ObserveTimeDrift ghi nhận độ lệch thời gian hệ thống và cập nhật trạng thái one-hot tương ứng.
//
// 🎯 LOGIC THỰC THI & TỐI ƯU HÓA:
//   - Thiết lập giá trị độ lệch thực tế lên `timeDriftGauge`.
//   - Duyệt qua slice static `timeSyncStates` (tránh cấp phát bộ nhớ runtime) để thiết lập
//     giá trị one-hot: Trạng thái khớp với `state` hiện tại nhận giá trị 1.0, các trạng thái khác nhận 0.0.
func (p *Prometheus) ObserveTimeDrift(seconds float64, state string) {
	if p == nil || p.timeDriftGauge == nil || p.timeSyncStateGauge == nil {
		return
	}
	p.timeDriftGauge.Set(seconds)

	// Thiết lập trạng thái one-hot một cách an toàn và tối ưu bộ nhớ
	for _, s := range timeSyncStates {
		v := 0.0
		if s == state {
			v = 1.0
		}
		p.timeSyncStateGauge.WithLabelValues(s).Set(v)
	}
}

// CurrentPrometheus trả về thực thể Prometheus đang hoạt động toàn cục.
func CurrentPrometheus() *Prometheus {
	return currentPrometheus.Load()
}

// ClearCurrentPrometheus dọn dẹp và ngắt kết nối thực thể Prometheus toàn cục.
func ClearCurrentPrometheus() {
	currentPrometheus.Store(nil)
}

// HTTPHandler xuất bản đối tượng promhttp.Handler chuẩn hóa từ Registry hiện tại.
//
// 🎯 CONTRACT & MECHANISM:
//   - Nếu Registry chưa sẵn sàng hoặc bị lỗi, trả về HTTP Handler phản hồi mã trạng thái 503
//     (StatusServiceUnavailable) một cách an toàn thay vì gây crash ứng dụng.
func (p *Prometheus) HTTPHandler() http.Handler {
	if p == nil || p.registry == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		})
	}
	return promhttp.HandlerFor(p.registry, promhttp.HandlerOpts{})
}

// IncInFlight tăng giá trị của số lượng in-flight requests đang xử lý hiện thời thêm 1 đơn vị.
func (p *Prometheus) IncInFlight() {
	if p != nil && p.inFlight != nil {
		p.inFlight.Inc()
	}
}

// DecInFlight giảm giá trị của số lượng in-flight requests đang xử lý hiện thời đi 1 đơn vị.
func (p *Prometheus) DecInFlight() {
	if p != nil && p.inFlight != nil {
		p.inFlight.Dec()
	}
}

// ObserveRequest thực hiện ghi nhận số liệu thống kê HTTP API request.
//
// 🎯 LOGIC THỰC THI & TỐI ƯU HÓA:
//   - Kiểm tra phòng thủ (Defensive validation) tránh nil pointer.
//   - Chuẩn hóa chuỗi và gán các nhãn (Labels) mặc định an toàn cho các tham số trống:
//   - Nếu Route trống, gán mặc định thành `"/"`.
//   - Nếu Method trống, gán mặc định thành `"UNKNOWN"`.
//   - Nếu Status trống, gán mặc định thành `"0"`.
//   - Gọi Vector.Inc() và Vector.Observe() để ghi chép số liệu đo lường.
func (p *Prometheus) ObserveRequest(method, route, status string, duration time.Duration) {
	if p == nil || p.requestTotal == nil || p.requestDuration == nil {
		return
	}
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
	p.requestTotal.WithLabelValues(method, route, status).Inc()
	p.requestDuration.WithLabelValues(method, route, status).Observe(duration.Seconds())
}

// ObserveDB là wrapper thuận tiện để đo lường và theo dõi latency các truy vấn PostgreSQL Database.
func (p *Prometheus) ObserveDB(operation string, duration time.Duration, err error) {
	p.observeDependency("db", operation, duration, err)
}

// ObserveRedis là wrapper thuận tiện để đo lường và theo dõi latency các lệnh thao tác Redis.
func (p *Prometheus) ObserveRedis(operation string, duration time.Duration, err error) {
	p.observeDependency("redis", operation, duration, err)
}

// observeDependency ghi nhận độ trễ và phân loại kết quả (ok / error) của các phụ thuộc dịch vụ bên ngoài.
//
// 🎯 LOGIC THỰC THI:
//   - Tự động chuẩn hóa tên phụ thuộc và thao tác.
//   - Phân loại trạng thái (`ok` hoặc `error`) dựa trên việc kiểm tra lỗi `err != nil`.
//   - Ghi nhận latency dạng giây vào Histogram.
func (p *Prometheus) observeDependency(kind, operation string, duration time.Duration, err error) {
	if p == nil || p.dependencyDur == nil {
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
	p.dependencyDur.WithLabelValues(kind, operation, status).Observe(duration.Seconds())
}

// ObserveAdminAction tăng bộ đếm Counter khi có các thao tác quản trị (Admin/Audit actions) trong hệ thống.
func (p *Prometheus) ObserveAdminAction(resource, action, result string) {
	if p == nil || p.requestTotal == nil {
		return
	}
	p.requestTotal.WithLabelValues("admin", resource+"."+action, result).Inc()
}
