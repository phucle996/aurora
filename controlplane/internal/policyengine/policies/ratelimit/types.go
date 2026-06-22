// ============================================================================
// 📂 FILE: policies/ratelimit/types.go - Định Nghĩa Mô Hình Cấu Hình Rate Limit
// ============================================================================
//
// 📌 VAI TRÒ (ROLE):
//   - Định nghĩa toàn bộ schema YAML đầu vào (raw) và cấu trúc Compiled (runtime)
//     cho hệ thống giới hạn tần suất yêu cầu (Rate Limiter Subsystem).
//   - Đảm bảo ánh xạ chính xác các giới hạn bucket và các cấu hình hành vi (fail-open/fail-closed).
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Trường `policies.rate_limit` trong tệp cấu hình động `runtime/policies/policy.yaml`.
//
// 🔒 RANH GIỚI BẢO MẬT/NGHIỆP VỤ (BOUNDARY):
//   - Xác lập ngưỡng chịu tải tối đa của hệ thống (inflight request, capacity, refill rate).
//   - Cách ly và bảo vệ các API nghiệp vụ nhạy cảm khỏi các cuộc tấn công DDoS/Brute-force.
//
// 🔄 CALLSITE FLOW:
//   - Tải từ YAML qua struct `RateLimitPolicy`.
//   - Lớp Middleware bảo mật truy xuất qua struct `CompiledPolicy` để kiểm tra quota cho mỗi Request.
//
// ============================================================================

package ratelimit

// RateLimitPolicy là cấu trúc cấu hình thô được map trực tiếp từ tệp YAML.
// RateLimitPolicy là cấu trúc cấu hình thô được map trực tiếp từ tệp YAML.
type RateLimitPolicy struct {
	// [COMMENT]: Loại bỏ cấu hình preauth để chuyển giao hoàn toàn trách nhiệm rate limit IP thô sang Envoy Gateway
	PostAuth      RateLimitPostAuthPolicy      `yaml:"postauth"`
	Observability RateLimitObservabilityPolicy `yaml:"observability"`
	Behavior      RateLimitBehaviorPolicy      `yaml:"behavior"`
}

type RateLimitPostAuthPolicy struct {
	Rules []RateLimitPathRule `yaml:"rules"`
}

type RateLimitPathRule struct {
	Path          string `yaml:"path"`
	Capacity      int64  `yaml:"capacity"`
	Refill        int64  `yaml:"refill"`
	PeriodSeconds int64  `yaml:"period_seconds"`
}

type RateLimitBucketPolicy struct {
	Capacity      int64 `yaml:"capacity"`
	Refill        int64 `yaml:"refill"`
	PeriodSeconds int64 `yaml:"period_seconds"`
}

type RateLimitObservabilityPolicy struct {
	SamplingPercent RateLimitSamplingPercentPolicy `yaml:"sampling_percent"`
}

type RateLimitSamplingPercentPolicy struct {
	Throttle           int `yaml:"throttle"`
	TemporaryIsolation int `yaml:"temporary_isolation"`
	Block              int `yaml:"block"`
	Error              int `yaml:"error"`
}

type RateLimitBehaviorPolicy struct {
	RetryAfterFallbackSeconds int64    `yaml:"retry_after_fallback_seconds"`
	FailOpen                  bool     `yaml:"fail_open"`
	BypassRoutePatterns       []string `yaml:"bypass_route_patterns"`
}

// ----------------------------------------------------------------------------
// Compiled Runtime Types
// ----------------------------------------------------------------------------

// CompiledPolicy chứa cấu hình Rate Limit đã được kiểm tra tính hợp lệ nghiêm ngặt ở runtime.
type CompiledPolicy struct {
	// [COMMENT]: Loại bỏ Compiled PreAuth do toàn bộ quá trình rate limit thô đã được Envoy offload
	PostAuth      CompiledRateLimitPostAuthPolicy
	Observability CompiledRateLimitObservabilityPolicy
	Behavior      CompiledRateLimitBehaviorPolicy
}

type CompiledRateLimitPostAuthPolicy struct {
	Rules []CompiledRateLimitPathRule
}

type CompiledRateLimitPathRule struct {
	Path          string
	Capacity      int64
	Refill        int64
	PeriodSeconds int64
}

type CompiledRateLimitBucketPolicy struct {
	Capacity      int64
	Refill        int64
	PeriodSeconds int64
}

type CompiledRateLimitObservabilityPolicy struct {
	SamplingPercent CompiledRateLimitSamplingPercentPolicy
}

type CompiledRateLimitSamplingPercentPolicy struct {
	Throttle           int
	TemporaryIsolation int
	Block              int
	Error              int
}

type CompiledRateLimitBehaviorPolicy struct {
	RetryAfterFallbackSeconds int64
	FailOpen                  bool
	BypassRoutePatterns       []string
}
