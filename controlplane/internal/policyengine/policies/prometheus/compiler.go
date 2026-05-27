// ============================================================================
// 📂 FILE: policies/prometheus/compiler.go - Trình Biên Dịch Chính Sách Prometheus
// ============================================================================
//
// 📌 VAI TRÒ (ROLE):
//   - Đảm nhận nhiệm vụ kiểm tra cú pháp và xác thực logic nghiêm ngặt (Strict Validation)
//     cho cấu hình Prometheus thô (YAML).
//   - Chuyển đổi từ mô hình dữ liệu cấu hình YAML (`PrometheusPolicy`) sang mô hình dữ liệu
//     runtime đã được xác thực an toàn (`CompiledPolicy`).
//   - Nếu có bất kỳ cấu hình lỗi nào (URL sai, Timeout hỏng, FailStrategy không khớp),
//     trình biên dịch sẽ báo lỗi lập tức để kích hoạt cơ chế LKG (Last-Known-Good) của Engine.
//
// ============================================================================

package prometheus

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

var (
	ErrInvalidFailStrategy = errors.New("prometheus policy: fail_strategy must be either 'fail_open' or 'fail_close'")
	ErrEmptyBaseURL        = errors.New("prometheus policy: query_client base_url must not be empty when enabled")
	ErrInvalidBaseURL      = errors.New("prometheus policy: query_client base_url is not a valid HTTP/HTTPS URL")
	ErrEmptyRoutePath      = errors.New("prometheus policy: expose_metrics route_path must not be empty when enabled")
	ErrInvalidRoutePath    = errors.New("prometheus policy: expose_metrics route_path must start with '/'")
)

// Compile thực hiện parse và validate cấu hình thô từ YAML sang runtime compiled policy.
func Compile(raw PrometheusPolicy) (CompiledPolicy, error) {
	compiled := CompiledPolicy{
		Enabled: raw.Enabled,
	}

	// Nếu Prometheus bị tắt hoặc chưa cấu hình, bỏ qua xác thực để đảm bảo tương thích ngược
	if !raw.Enabled {
		return compiled, nil
	}

	// 1. Xác thực FailStrategy (Không tự động gán mặc định để kích hoạt LKG nếu nhập sai)
	strategy := strings.TrimSpace(strings.ToLower(raw.FailStrategy))
	if strategy != "fail_open" && strategy != "fail_close" {
		return CompiledPolicy{}, fmt.Errorf("%w (got: '%s')", ErrInvalidFailStrategy, raw.FailStrategy)
	}
	compiled.FailStrategy = strategy

	// 2. Xác thực Query Client (nếu Enabled)
	compiled.QueryClient.Enabled = raw.QueryClient.Enabled
	if raw.QueryClient.Enabled {
		baseURL := strings.TrimSpace(raw.QueryClient.BaseURL)
		if baseURL == "" {
			return CompiledPolicy{}, ErrEmptyBaseURL
		}
		parsedURL, err := url.ParseRequestURI(baseURL)
		if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
			return CompiledPolicy{}, fmt.Errorf("%w: %s", ErrInvalidBaseURL, baseURL)
		}
		compiled.QueryClient.BaseURL = baseURL

		// Parse timeouts
		qTimeout, err := time.ParseDuration(raw.QueryClient.QueryTimeout)
		if err != nil {
			return CompiledPolicy{}, fmt.Errorf("prometheus policy: invalid query_timeout duration '%s': %w", raw.QueryClient.QueryTimeout, err)
		}
		compiled.QueryClient.QueryTimeout = qTimeout

		dStep, err := time.ParseDuration(raw.QueryClient.DefaultStep)
		if err != nil {
			return CompiledPolicy{}, fmt.Errorf("prometheus policy: invalid default_step duration '%s': %w", raw.QueryClient.DefaultStep, err)
		}
		compiled.QueryClient.DefaultStep = dStep
	}

	// 3. Xác thực Expose Metrics (nếu Enabled)
	compiled.ExposeMetric.Enabled = raw.ExposeMetric.Enabled
	if raw.ExposeMetric.Enabled {
		routePath := strings.TrimSpace(raw.ExposeMetric.RoutePath)
		if routePath == "" {
			return CompiledPolicy{}, ErrEmptyRoutePath
		}
		if !strings.HasPrefix(routePath, "/") {
			return CompiledPolicy{}, fmt.Errorf("%w: '%s'", ErrInvalidRoutePath, routePath)
		}
		compiled.ExposeMetric.RoutePath = routePath
	}

	return compiled, nil
}
