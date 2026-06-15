// ============================================================================
// 📂 MODULE: middleware/metrics/register.go - Đăng Ký Middleware Metrics (OTel)
// ============================================================================
// Quản lý OTel instruments cho auth requests, cache operations, và rate limit
// tại tầng middleware. Lazy init qua sync.Once.
// ============================================================================

package middleware_metrics

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	initOnce sync.Once

	// authRequestsCounter đếm tổng số lần xác thực ở các middleware.
	authRequestsCounter metric.Int64Counter

	// cacheOperationsCounter đếm tổng số lần thao tác với cache ở các middleware.
	cacheOperationsCounter metric.Int64Counter

	// rlCheckTotal đếm tổng số check rate limit, phân nhóm theo route/scope/result.
	rlCheckTotal metric.Int64Counter

	// rlDecisionTotal đếm tổng số quyết định chặn/phạt (Throttle/Block/Isolation).
	rlDecisionTotal metric.Int64Counter

	// rlErrorTotal đếm tổng lỗi trong quá trình đánh giá (Redis die, v.v.).
	rlErrorTotal metric.Int64Counter

	// rlEvalDuration ghi nhận latency của evaluator.
	rlEvalDuration metric.Float64Histogram

	// rlRetryAfter phân phối Retry-After trả về cho Client.
	rlRetryAfter metric.Float64Histogram

	// rlLocalCacheTotal đếm hoạt động trên Local Deny Cache (Hit/Evict/Add).
	rlLocalCacheTotal metric.Int64Counter
)

// ensureInit khởi tạo OTel instruments cho middleware metrics.
func ensureInit() {
	initOnce.Do(func() {
		meter := otel.Meter("aurora-controlplane.middleware")

		authRequestsCounter, _ = meter.Int64Counter(
			"aurora_controlplane_middleware_auth_requests_total",
			metric.WithDescription("Total authentication attempts in HTTP middlewares."),
		)
		cacheOperationsCounter, _ = meter.Int64Counter(
			"aurora_controlplane_middleware_cache_operations_total",
			metric.WithDescription("Total cache operations in HTTP middlewares."),
		)

		rlCheckTotal, _ = meter.Int64Counter(
			"security_ratelimit_check_total",
			metric.WithDescription("Total number of rate limit checks by route/rule/result."),
		)
		rlDecisionTotal, _ = meter.Int64Counter(
			"security_ratelimit_decision_total",
			metric.WithDescription("Total number of final rate limit decisions by route/scope."),
		)
		rlErrorTotal, _ = meter.Int64Counter(
			"security_ratelimit_error_total",
			metric.WithDescription("Total number of rate limit evaluation errors by route/error type."),
		)
		rlEvalDuration, _ = meter.Float64Histogram(
			"security_ratelimit_eval_duration_seconds",
			metric.WithDescription("Rate limit evaluator duration by rule scope."),
		)
		rlRetryAfter, _ = meter.Float64Histogram(
			"security_ratelimit_retry_after_seconds",
			metric.WithDescription("Retry-After seconds returned by rate limiter."),
		)
		rlLocalCacheTotal, _ = meter.Int64Counter(
			"security_ratelimit_local_cache_total",
			metric.WithDescription("Local deny-cache events by action and rule scope."),
		)
	})
}

// RecordAuthAttempt ghi nhận một lượt xác thực qua middleware.
func RecordAuthAttempt(middleware, status, reason string) {
	ensureInit()
	if authRequestsCounter != nil {
		authRequestsCounter.Add(context.Background(), 1,
			metric.WithAttributes(
				attribute.String("middleware", middleware),
				attribute.String("status", status),
				attribute.String("reason", reason),
			),
		)
	}
}

// RecordCacheOperation ghi nhận một thao tác đọc/ghi cache ở tầng middleware.
func RecordCacheOperation(middleware, cacheName, level, outcome string) {
	ensureInit()
	if cacheOperationsCounter != nil {
		cacheOperationsCounter.Add(context.Background(), 1,
			metric.WithAttributes(
				attribute.String("middleware", middleware),
				attribute.String("cache_name", cacheName),
				attribute.String("level", level),
				attribute.String("outcome", outcome),
			),
		)
	}
}

// RecordRLCheck ghi nhận một lượt kiểm tra rate limit.
func RecordRLCheck(routePattern, ruleScope, result string) {
	ensureInit()
	if rlCheckTotal != nil {
		rlCheckTotal.Add(context.Background(), 1, metric.WithAttributes(
			attribute.String("route_pattern", routePattern),
			attribute.String("rule_scope", ruleScope),
			attribute.String("result", result),
		))
	}
}

// RecordRLDecision ghi nhận một quyết định chặn/phạt của rate limiter.
func RecordRLDecision(routePattern, decision, ruleScope string) {
	ensureInit()
	if rlDecisionTotal != nil {
		rlDecisionTotal.Add(context.Background(), 1, metric.WithAttributes(
			attribute.String("route_pattern", routePattern),
			attribute.String("decision", decision),
			attribute.String("rule_scope", ruleScope),
		))
	}
}

// RecordRLError ghi nhận một lỗi xảy ra trong quá trình kiểm tra rate limit.
func RecordRLError(routePattern, errorType string) {
	ensureInit()
	if rlErrorTotal != nil {
		rlErrorTotal.Add(context.Background(), 1, metric.WithAttributes(
			attribute.String("route_pattern", routePattern),
			attribute.String("error_type", errorType),
		))
	}
}

// RecordRLEvalDuration ghi nhận latency của evaluator.
func RecordRLEvalDuration(ruleScope string, duration time.Duration) {
	ensureInit()
	if rlEvalDuration != nil {
		rlEvalDuration.Record(context.Background(), duration.Seconds(), metric.WithAttributes(
			attribute.String("rule_scope", ruleScope),
		))
	}
}

// RecordRLRetryAfter ghi nhận Retry-After trả về cho Client.
func RecordRLRetryAfter(routePattern string, retryAfter time.Duration) {
	ensureInit()
	if rlRetryAfter != nil && retryAfter > 0 {
		rlRetryAfter.Record(context.Background(), retryAfter.Seconds(), metric.WithAttributes(
			attribute.String("route_pattern", routePattern),
		))
	}
}

// RecordRLLocalCache ghi nhận hoạt động trên Local Deny Cache.
func RecordRLLocalCache(action, ruleScope string) {
	ensureInit()
	if rlLocalCacheTotal != nil {
		rlLocalCacheTotal.Add(context.Background(), 1, metric.WithAttributes(
			attribute.String("action", action),
			attribute.String("rule_scope", ruleScope),
		))
	}
}
