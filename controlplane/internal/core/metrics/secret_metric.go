// ============================================================================
// 📂 MODULE: controlplane/internal/core/metrics/secret_metric.go
//            Public API cho Secret Rotation / Lifecycle / Auth Fallback Metrics
// ============================================================================
// Các hàm Observe public được gọi từ service layer. Chuẩn hóa input trước khi
// ghi nhận vào OTel instruments đã khởi tạo trong module_register.go.
// ============================================================================

package coreMetric

import (
	"strings"
	"time"
)

// ObserveSecretLifecycle ghi nhận sự kiện vòng đời secret với thời gian bắt đầu.
func ObserveSecretLifecycle(operation string, family string, result string, startedAt time.Time) {
	// Chuẩn hóa input trước khi ghi metrics
	operation = strings.TrimSpace(operation)
	if operation == "" {
		operation = "unknown"
	}
	family = strings.TrimSpace(family)
	if family == "" {
		family = "unknown"
	}
	result = strings.TrimSpace(result)
	if result == "" {
		result = "unknown"
	}

	// Delegate sang hàm trong module_register.go đã sử dụng OTel instruments
	duration := time.Since(startedAt)
	ensureInit()
	observeSecretLifecycleInternal(operation, family, result, duration)
}

// ObserveSecretRotationSuccess ghi nhận rotation thành công.
func ObserveSecretRotationSuccess(family string) {
	family = strings.TrimSpace(family)
	if family == "" {
		family = "unknown"
	}
	ObserveSecretRotationSuccessOTel(family)
}

// ObserveSecretRotationFailure ghi nhận rotation thất bại.
func ObserveSecretRotationFailure(family string) {
	family = strings.TrimSpace(family)
	if family == "" {
		family = "unknown"
	}
	ObserveSecretRotationFailureOTel(family)
}

// ObserveAuthTokenVerifyFallback ghi nhận token verify fallback path.
func ObserveAuthTokenVerifyFallback(family, versionState string) {
	family = strings.TrimSpace(family)
	if family == "" {
		family = "unknown"
	}
	versionState = strings.TrimSpace(versionState)
	if versionState == "" {
		versionState = "unknown"
	}
	ObserveAuthTokenFallback(family, versionState)
}
