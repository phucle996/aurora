package coreMetric

import (
	"strings"
	"time"
)

func ObserveSecretLifecycle(operation string, family string, result string, startedAt time.Time) {
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
	if secretLifecycleTotalCounter == nil || secretLifecycleDurHistogram == nil {
		return
	}
	secretLifecycleTotalCounter.WithLabelValues(operation, family, result).Inc()
	secretLifecycleDurHistogram.WithLabelValues(operation, family, result).Observe(time.Since(startedAt).Seconds())
}

func ObserveSecretRotationSuccess(family string) {
	if secretRotationSuccessCounter == nil {
		return
	}
	family = strings.TrimSpace(family)
	if family == "" {
		family = "unknown"
	}
	secretRotationSuccessCounter.WithLabelValues(family).Inc()
}

func ObserveSecretRotationFailure(family string) {
	if secretRotationFailureCounter == nil {
		return
	}
	family = strings.TrimSpace(family)
	if family == "" {
		family = "unknown"
	}
	secretRotationFailureCounter.WithLabelValues(family).Inc()
}

func ObserveAuthTokenVerifyFallback(family, versionState string) {
	if authTokenVerifyFallbackCount == nil {
		return
	}
	family = strings.TrimSpace(family)
	if family == "" {
		family = "unknown"
	}
	versionState = strings.TrimSpace(versionState)
	if versionState == "" {
		versionState = "unknown"
	}
	authTokenVerifyFallbackCount.WithLabelValues(family, versionState).Inc()
}
