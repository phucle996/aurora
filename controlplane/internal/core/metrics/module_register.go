package coreMetric

import (
	"strings"
	"sync"

	"controlplane/internal/observability"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	registerOnce sync.Once

	secretRotationSuccessCounter *prometheus.CounterVec
	secretRotationFailureCounter *prometheus.CounterVec
	secretLifecycleTotalCounter  *prometheus.CounterVec
	secretLifecycleDurHistogram  *prometheus.HistogramVec
	authTokenVerifyFallbackCount *prometheus.CounterVec
)

func Register(registry *prometheus.Registry, namespace string) error {
	var registerErr error
	registerOnce.Do(func() {
		namespace = normalizeNamespace(namespace)

		InitDataplaneMetrics(namespace)

		secretRotationSuccessCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "iam",
			Name:      "secret_rotation_success_total",
			Help:      "Successful secret rotations by family.",
		}, []string{"family"})

		secretRotationFailureCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "iam",
			Name:      "secret_rotation_failure_total",
			Help:      "Failed secret rotations by family.",
		}, []string{"family"})

		secretLifecycleTotalCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "core",
			Name:      "secret_lifecycle_total",
			Help:      "Secret lifecycle events by operation family and result.",
		}, []string{"operation", "family", "result"})

		secretLifecycleDurHistogram = prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: "core",
			Name:      "secret_lifecycle_duration_seconds",
			Help:      "Secret lifecycle latency by operation family and result.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"operation", "family", "result"})

		authTokenVerifyFallbackCount = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "iam",
			Name:      "auth_token_verify_fallback_total",
			Help:      "Token verification fallback path usage by family and version state.",
		}, []string{"family", "version_state"})

		for _, collector := range []prometheus.Collector{
			secretRotationSuccessCounter,
			secretRotationFailureCounter,
			secretLifecycleTotalCounter,
			secretLifecycleDurHistogram,
			authTokenVerifyFallbackCount,
			dataplaneHeartbeatTotal,
		} {
			if err := registry.Register(collector); err != nil {
				registerErr = err
				return
			}
		}
	})
	return registerErr
}

func normalizeNamespace(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	if value == "" {
		return "aurora_controlplane"
	}
	return value
}

func init() {
	observability.RegisterModuleMetrics(Register)
}
