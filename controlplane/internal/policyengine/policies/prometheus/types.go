// ============================================================================
// 📂 FILE: policies/prometheus/types.go - Định Nghĩa Mô Hình Cấu Hình Prometheus
// ============================================================================
//
// 📌 VAI TRÒ (ROLE):
//   - Định nghĩa schema cấu hình thô YAML (raw) và cấu trúc biên dịch Compiled (runtime)
//     cho hệ thống giám sát Prometheus (Prometheus Telemetry Infrastructure).
//   - Hỗ trợ bật/tắt động, chiến lược Fail-Open/Fail-Close, cấu hình Query Client,
//     và lộ trình xuất bản metrics (Expose Metrics route path).
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Trường `policies.prometheus` trong tệp cấu hình động `runtime/policies/policy.yaml`.
//
// ============================================================================

package prometheus

import "time"

// PrometheusPolicy đại diện cho cấu trúc thô (raw) được ánh xạ trực tiếp từ file YAML.
type PrometheusPolicy struct {
	Enabled      bool              `yaml:"enabled"`
	FailStrategy string            `yaml:"fail_strategy"` // "fail_open" hoặc "fail_close"
	QueryClient  QueryClientPolicy `yaml:"query_client"`
	ExposeMetric ExposePolicy      `yaml:"expose_metrics"`
}

// QueryClientPolicy chứa cấu hình thô phục vụ việc kết nối truy vấn dữ liệu ngược từ Prometheus.
type QueryClientPolicy struct {
	Enabled      bool   `yaml:"enabled"`
	BaseURL      string `yaml:"base_url"`
	QueryTimeout string `yaml:"query_timeout"`
	DefaultStep  string `yaml:"default_step"`
}

// ExposePolicy chứa cấu hình thô phục vụ việc xuất bản endpoint cào metrics.
type ExposePolicy struct {
	Enabled   bool   `yaml:"enabled"`
	RoutePath string `yaml:"route_path"`
}

// ----------------------------------------------------------------------------
// Compiled Runtime Types
// ----------------------------------------------------------------------------

// CompiledPolicy đại diện cho cấu hình Prometheus đã được xác thực an toàn ở runtime.
type CompiledPolicy struct {
	Enabled      bool
	FailStrategy string
	QueryClient  CompiledQueryClientPolicy
	ExposeMetric CompiledExposePolicy
}

// CompiledQueryClientPolicy chứa cấu hình kết nối truy vấn đã được biên dịch & xác thực.
type CompiledQueryClientPolicy struct {
	Enabled      bool
	BaseURL      string
	QueryTimeout time.Duration
	DefaultStep  time.Duration
}

// CompiledExposePolicy chứa cấu hình xuất bản metrics đã được xác thực.
type CompiledExposePolicy struct {
	Enabled   bool
	RoutePath string
}
