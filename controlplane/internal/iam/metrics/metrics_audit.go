package iamMetrics

import "github.com/prometheus/client_golang/prometheus"

var (
	auditPublishTotalCounter *prometheus.CounterVec
	loginLastSeenFlushTotal  *prometheus.CounterVec
)

func registerAuditMetrics(registry *prometheus.Registry, namespace string) error {
	auditPublishTotalCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "iam",
		Name:      "audit_publish_total",
		Help:      "Async audit publish outcomes by event and result.",
	}, []string{"event", "result"})

	loginLastSeenFlushTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "iam",
		Name:      "login_last_seen_flush_total",
		Help:      "Login last_seen flush outcomes (db|skip|cache_hit).",
	}, []string{"result"})

	for _, c := range []prometheus.Collector{auditPublishTotalCounter, loginLastSeenFlushTotal} {
		if err := registry.Register(c); err != nil {
			return err
		}
	}
	return nil
}

func ObserveAuditPublish(event string, result string) {
	if auditPublishTotalCounter == nil {
		return
	}
	auditPublishTotalCounter.WithLabelValues(event, result).Inc()
}

func ObserveLoginLastSeenFlush(result string) {
	if loginLastSeenFlushTotal == nil {
		return
	}
	loginLastSeenFlushTotal.WithLabelValues(result).Inc()
}
