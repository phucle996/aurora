package iamMetrics

import (
	"time"

	"controlplane/internal/observability"

	"github.com/prometheus/client_golang/prometheus"
)

var adminKeyRotationTotalCounter *prometheus.CounterVec
var adminLoginTotalCounter *prometheus.CounterVec
var adminRefreshTotalCounter *prometheus.CounterVec

func registerAdminMetrics(registry *prometheus.Registry, namespace string) error {
	adminLoginTotalCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "iam",
		Name:      "admin_login_total",
		Help:      "Admin login outcomes by result.",
	}, []string{"result"})

	adminRefreshTotalCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "iam",
		Name:      "admin_refresh_total",
		Help:      "Admin refresh outcomes by result.",
	}, []string{"result"})

	adminKeyRotationTotalCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "iam",
		Name:      "admin_key_rotation_total",
		Help:      "Admin key rotation scheduler outcomes by result.",
	}, []string{"result"})

	for _, collector := range []prometheus.Collector{adminLoginTotalCounter, adminRefreshTotalCounter, adminKeyRotationTotalCounter} {
		if err := registry.Register(collector); err != nil {
			return err
		}
	}
	return nil
}

func ObserveAdminLoginOutcome(result string) {
	if adminLoginTotalCounter == nil {
		return
	}
	result = normalizeResult(result)
	adminLoginTotalCounter.WithLabelValues(result).Inc()
	observeAuthAttempt("admin_login", result == OutcomeSuccess)
}

func ObserveAdminKeyRotationOutcome(result string) {
	if adminKeyRotationTotalCounter == nil {
		return
	}
	result = normalizeResult(result)
	adminKeyRotationTotalCounter.WithLabelValues(result).Inc()
}

const adminRefreshFlowName = "admin_refresh"

func ObserveAdminRefreshOutcome(result string) {
	result = normalizeResult(result)
	if adminRefreshTotalCounter != nil {
		adminRefreshTotalCounter.WithLabelValues(result).Inc()
	}
	observeAuthAttempt(adminRefreshFlowName, result == OutcomeSuccess)
}

func ObserveAdminRefreshLatency(duration time.Duration, err error) {
	if prom := observability.CurrentPrometheus(); prom != nil {
		prom.ObserveRedis("iam.admin.refresh", duration, err)
	}
}
