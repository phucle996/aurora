package middleware_metrics

import (
	"sync"

	"controlplane/internal/observability"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	registerOnce sync.Once

	// authRequestsCounter đếm tổng số lần xác thực ở các middleware.
	// FQN: aurora_controlplane_middleware_auth_requests_total
	authRequestsCounter *prometheus.CounterVec

	// cacheOperationsCounter đếm tổng số lần thao tác với cache ở các middleware.
	// FQN: aurora_controlplane_middleware_cache_operations_total
	cacheOperationsCounter *prometheus.CounterVec
)

// Register đăng ký metrics của middleware vào Prometheus Registry.
func Register(registry *prometheus.Registry, namespace string) error {
	var registerErr error
	registerOnce.Do(func() {
		authRequestsCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "middleware",
			Name:      "auth_requests_total",
			Help:      "Total authentication attempts in HTTP middlewares, partitioned by middleware name, status and reason.",
		}, []string{"middleware", "status", "reason"})

		cacheOperationsCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "middleware",
			Name:      "cache_operations_total",
			Help:      "Total cache operations in HTTP middlewares, partitioned by middleware name, cache name, level and outcome.",
		}, []string{"middleware", "cache_name", "level", "outcome"})

		for _, c := range []prometheus.Collector{authRequestsCounter, cacheOperationsCounter} {
			if err := registry.Register(c); err != nil {
				registerErr = err
				return
			}
		}
	})
	return registerErr
}

func init() {
	// Tự đăng ký module metrics vào callback chain của observability.
	observability.RegisterModuleMetrics(Register)
}
