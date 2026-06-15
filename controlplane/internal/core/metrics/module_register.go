// ============================================================================
// 📂 MODULE: controlplane/internal/core/metrics/module_register.go
//            Đo Lường Nghiệp Vụ Module Core (OTel Metrics)
// ============================================================================
// Quản lý metrics cho: Secret Rotation, Secret Lifecycle, Auth Token Fallback.
// Sử dụng native OTel instruments, lazy init qua sync.Once.
// ============================================================================

package coreMetric

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

	// Secret Rotation metrics
	secretRotationSuccessCounter metric.Int64Counter
	secretRotationFailureCounter metric.Int64Counter

	// Secret Lifecycle metrics
	secretLifecycleTotalCounter metric.Int64Counter
	secretLifecycleDurHistogram metric.Float64Histogram

	// Auth Token Verify Fallback
	authTokenVerifyFallbackCount metric.Int64Counter
)

// ensureInit khởi tạo OTel instruments một lần duy nhất an toàn đa luồng.
func ensureInit() {
	initOnce.Do(func() {
		meter := otel.Meter("aurora-controlplane.core")

		secretRotationSuccessCounter, _ = meter.Int64Counter(
			"aurora_controlplane_iam_secret_rotation_success_total",
			metric.WithDescription("Successful secret rotations by family."),
		)
		secretRotationFailureCounter, _ = meter.Int64Counter(
			"aurora_controlplane_iam_secret_rotation_failure_total",
			metric.WithDescription("Failed secret rotations by family."),
		)
		secretLifecycleTotalCounter, _ = meter.Int64Counter(
			"aurora_controlplane_core_secret_lifecycle_total",
			metric.WithDescription("Secret lifecycle events by operation family and result."),
		)
		secretLifecycleDurHistogram, _ = meter.Float64Histogram(
			"aurora_controlplane_core_secret_lifecycle_duration_seconds",
			metric.WithDescription("Secret lifecycle latency by operation family and result."),
		)
		authTokenVerifyFallbackCount, _ = meter.Int64Counter(
			"aurora_controlplane_iam_auth_token_verify_fallback_total",
			metric.WithDescription("Token verification fallback path usage by family and version state."),
		)

		// Khởi tạo Zone metrics cùng lúc
		initZoneMetrics(meter)
	})
}

// ObserveSecretRotationSuccessOTel ghi nhận rotation thành công theo family (internal).
func ObserveSecretRotationSuccessOTel(family string) {
	ensureInit()
	if secretRotationSuccessCounter != nil {
		secretRotationSuccessCounter.Add(context.Background(), 1,
			metric.WithAttributes(attribute.String("family", family)),
		)
	}
}

// ObserveSecretRotationFailureOTel ghi nhận rotation thất bại theo family (internal).
func ObserveSecretRotationFailureOTel(family string) {
	ensureInit()
	if secretRotationFailureCounter != nil {
		secretRotationFailureCounter.Add(context.Background(), 1,
			metric.WithAttributes(attribute.String("family", family)),
		)
	}
}

// observeSecretLifecycleInternal ghi nhận sự kiện vòng đời secret (internal, gọi từ secret_metric.go).
func observeSecretLifecycleInternal(operation, family, result string, duration time.Duration) {
	ensureInit()
	ctx := context.Background()
	attrs := metric.WithAttributes(
		attribute.String("operation", operation),
		attribute.String("family", family),
		attribute.String("result", result),
	)
	if secretLifecycleTotalCounter != nil {
		secretLifecycleTotalCounter.Add(ctx, 1, attrs)
	}
	if secretLifecycleDurHistogram != nil {
		secretLifecycleDurHistogram.Record(ctx, duration.Seconds(), attrs)
	}
}

// ObserveAuthTokenFallback ghi nhận token verify fallback path.
func ObserveAuthTokenFallback(family, versionState string) {
	ensureInit()
	if authTokenVerifyFallbackCount != nil {
		authTokenVerifyFallbackCount.Add(context.Background(), 1,
			metric.WithAttributes(
				attribute.String("family", family),
				attribute.String("version_state", versionState),
			),
		)
	}
}
