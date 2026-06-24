// ============================================================================
// 📂 MODULE: middleware/metrics/register.go - Đăng Ký Middleware Metrics (OTel)
// ============================================================================
// Quản lý OTel instruments cho auth requests và cache operations
// tại tầng middleware. Lazy init qua sync.Once.
// ============================================================================

package middleware_metrics

import (
	"context"
	"sync"

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
