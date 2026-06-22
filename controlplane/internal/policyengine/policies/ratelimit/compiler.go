// ============================================================================
// 📂 FILE: policies/ratelimit/compiler.go - Trình Biên Dịch & Xác Thực Rate Limit
// ============================================================================
//
// 📌 VAI TRÒ (ROLE):
//   - Biên dịch cấu hình thô từ YAML sang định dạng trung gian có kiểm soát (Compiled).
//   - Thực thi xác thực tham số nghiêm ngặt, đảm bảo các chỉ số tải luôn hợp lệ.
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Tệp [types.go](file:///home/phuckle/Desktop/New/controlplane/internal/policyengine/policies/ratelimit/types.go) trong cùng package.
//
// 🔒 RANH GIỚI BẢO MẬT/NGHIỆP VỤ (BOUNDARY):
//   - Chặn đứng các giá trị âm hoặc không hợp lệ của các tham số dung lượng (Capacity),
//     tốc độ đổ đầy (Refill), giới hạn hàng đợi, tỉ lệ lấy mẫu (Sampling percent).
//   - Đảm bảo danh sách bypass route patterns không bị rỗng để tránh làm sập luồng điều hướng.
//
// 🔄 CALLSITE FLOW:
//   - Được gọi bởi bộ điều phối chính `runtime.compilePolicies` khi tải hoặc nạp lại cấu hình.
//
// ============================================================================

package ratelimit

import (
	errorx "controlplane/internal/policyengine/errorx"
	"fmt"
	"strings"
)

// Compile phân tích cú pháp, chuyển đổi cấu hình thô và thực thi xác thực toàn bộ chính sách Rate Limit.
// Nếu phát hiện bất kỳ tham số nào không hợp lệ, hàm sẽ trả về lỗi `errorx.ErrPolicyInvalid`.
//
// # Tham số:
//   - `src`: Cấu trúc cấu hình thô `RateLimitPolicy` được đọc từ YAML.
//
// # Trả về:
//   - `CompiledPolicy`: Cấu hình Rate Limit đã được kiểm chứng an toàn.
//   - `error`: Lỗi nếu có thông số sai lệch.
func Compile(src RateLimitPolicy) (CompiledPolicy, error) {
	out := CompiledPolicy{}

	// [COMMENT]: Chỉ compile và validate các cấu hình PostAuth. Quy trình Pre-Auth đã chuyển hoàn toàn lên Envoy Gateway.
	for _, rule := range src.PostAuth.Rules {
		path := strings.TrimSpace(rule.Path)
		if path == "" {
			return CompiledPolicy{}, fmt.Errorf("%w: ratelimit: rule path cannot be empty", errorx.ErrPolicyInvalid)
		}
		if rule.Capacity <= 0 || rule.Refill <= 0 || rule.PeriodSeconds <= 0 {
			return CompiledPolicy{}, fmt.Errorf("%w: ratelimit: invalid parameters for path %s", errorx.ErrPolicyInvalid, path)
		}
		out.PostAuth.Rules = append(out.PostAuth.Rules, CompiledRateLimitPathRule{
			Path:          path,
			Capacity:      rule.Capacity,
			Refill:        rule.Refill,
			PeriodSeconds: rule.PeriodSeconds,
		})
	}

	// Biên dịch tham số Giám sát & Lấy mẫu (Sampling & Observability)
	out.Observability.SamplingPercent.Throttle = src.Observability.SamplingPercent.Throttle
	out.Observability.SamplingPercent.TemporaryIsolation = src.Observability.SamplingPercent.TemporaryIsolation
	out.Observability.SamplingPercent.Block = src.Observability.SamplingPercent.Block
	out.Observability.SamplingPercent.Error = src.Observability.SamplingPercent.Error

	if out.Observability.SamplingPercent.Throttle < 0 || out.Observability.SamplingPercent.Throttle > 100 {
		return CompiledPolicy{}, fmt.Errorf("%w: ratelimit: throttle sampling percent must be between 0 and 100", errorx.ErrPolicyInvalid)
	}
	if out.Observability.SamplingPercent.TemporaryIsolation < 0 || out.Observability.SamplingPercent.TemporaryIsolation > 100 {
		return CompiledPolicy{}, fmt.Errorf("%w: ratelimit: temporary isolation sampling percent must be between 0 and 100", errorx.ErrPolicyInvalid)
	}
	if out.Observability.SamplingPercent.Block < 0 || out.Observability.SamplingPercent.Block > 100 {
		return CompiledPolicy{}, fmt.Errorf("%w: ratelimit: block sampling percent must be between 0 and 100", errorx.ErrPolicyInvalid)
	}
	if out.Observability.SamplingPercent.Error < 0 || out.Observability.SamplingPercent.Error > 100 {
		return CompiledPolicy{}, fmt.Errorf("%w: ratelimit: error sampling percent must be between 0 and 100", errorx.ErrPolicyInvalid)
	}

	// Biên dịch các cấu hình hành vi khi lỗi (Failure mode behavior)
	out.Behavior.RetryAfterFallbackSeconds = src.Behavior.RetryAfterFallbackSeconds
	out.Behavior.FailOpen = src.Behavior.FailOpen
	if out.Behavior.RetryAfterFallbackSeconds <= 0 {
		return CompiledPolicy{}, fmt.Errorf("%w: ratelimit: retry_after_fallback_seconds must be positive", errorx.ErrPolicyInvalid)
	}

	for _, item := range src.Behavior.BypassRoutePatterns {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		out.Behavior.BypassRoutePatterns = append(out.Behavior.BypassRoutePatterns, trimmed)
	}
	if len(out.Behavior.BypassRoutePatterns) == 0 {
		return CompiledPolicy{}, fmt.Errorf("%w: ratelimit: bypass_route_patterns list cannot be empty", errorx.ErrPolicyInvalid)
	}

	return out, nil
}
