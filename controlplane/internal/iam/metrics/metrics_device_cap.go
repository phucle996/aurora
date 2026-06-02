package iamMetrics

import "github.com/prometheus/client_golang/prometheus"

var (
	deviceCapEvictTotalCounter    *prometheus.CounterVec
	deviceCapLockSkipTotalCounter prometheus.Counter
	deviceCapReconcileRunsTotal   *prometheus.CounterVec
)

func registerDeviceCapMetrics(registry *prometheus.Registry, namespace string) error {
	deviceCapEvictTotalCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "iam",
		Name:      "device_cap_evict_total",
		Help:      "Total devices evicted by cap-per-user policy, by reason.",
	}, []string{"reason"})

	deviceCapLockSkipTotalCounter = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "iam",
		Name:      "device_cap_lock_skip_total",
		Help:      "Login flow skipped cap evict due to lock contention. Reconciler will retry.",
	})

	deviceCapReconcileRunsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "iam",
		Name:      "device_cap_reconcile_runs_total",
		Help:      "Reconciler tick outcomes for cap-per-user policy.",
	}, []string{"result"})

	for _, c := range []prometheus.Collector{deviceCapEvictTotalCounter, deviceCapLockSkipTotalCounter, deviceCapReconcileRunsTotal} {
		if err := registry.Register(c); err != nil {
			return err
		}
	}
	return nil
}

// ObserveDeviceCapEvict tăng counter số device evicted ở 1 batch.
func ObserveDeviceCapEvict(reason string, count int) {
	if deviceCapEvictTotalCounter == nil || count <= 0 {
		return
	}
	deviceCapEvictTotalCounter.WithLabelValues(reason).Add(float64(count))
}

// ObserveDeviceCapLockSkip tăng counter mỗi lần login bỏ qua evict do lock bận.
func ObserveDeviceCapLockSkip() {
	if deviceCapLockSkipTotalCounter == nil {
		return
	}
	deviceCapLockSkipTotalCounter.Inc()
}

// ObserveDeviceCapReconcile tăng counter cho reconciler tick.
func ObserveDeviceCapReconcile(result string) {
	if deviceCapReconcileRunsTotal == nil {
		return
	}
	deviceCapReconcileRunsTotal.WithLabelValues(result).Inc()
}
