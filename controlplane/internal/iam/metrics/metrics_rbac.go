package iamMetrics

import (
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	rbacAuthorizeTotalCounter     *prometheus.CounterVec
	rbacAuthorizeDenyTotalCounter *prometheus.CounterVec
	rbacCacheHitTotalCounter      *prometheus.CounterVec
	rbacCacheMissTotalCounter     *prometheus.CounterVec
	rbacInvalidationTotalCounter  *prometheus.CounterVec
	rbacSyncTotalCounter          *prometheus.CounterVec
)

func registerRbacMetrics(registry *prometheus.Registry, namespace string) error {
	namespace = normalizeNamespace(namespace)

	rbacAuthorizeTotalCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "iam",
		Name:      "rbac_authorize_total",
		Help:      "RBAC authorize decisions by result.",
	}, []string{"result"})

	rbacAuthorizeDenyTotalCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "iam",
		Name:      "rbac_authorize_deny_total",
		Help:      "RBAC deny decisions by reason.",
	}, []string{"reason"})

	rbacCacheHitTotalCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "iam",
		Name:      "rbac_cache_hit_total",
		Help:      "RBAC cache hits by layer.",
	}, []string{"layer"})

	rbacCacheMissTotalCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "iam",
		Name:      "rbac_cache_miss_total",
		Help:      "RBAC cache misses by layer.",
	}, []string{"layer"})

	rbacInvalidationTotalCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "iam",
		Name:      "rbac_invalidation_total",
		Help:      "RBAC invalidation events by kind and result.",
	}, []string{"kind", "result"})

	rbacSyncTotalCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "iam",
		Name:      "rbac_sync_total",
		Help:      "RBAC cache sync iterations by result.",
	}, []string{"result"})

	for _, collector := range []prometheus.Collector{rbacAuthorizeTotalCounter, rbacAuthorizeDenyTotalCounter, rbacCacheHitTotalCounter, rbacCacheMissTotalCounter, rbacInvalidationTotalCounter, rbacSyncTotalCounter} {
		if err := registry.Register(collector); err != nil {
			return err
		}
	}
	return nil
}

func ObserveRbacAuthorize(result string) {
	if rbacAuthorizeTotalCounter == nil {
		return
	}
	result = strings.TrimSpace(strings.ToLower(result))
	if result == "" {
		result = "unknown"
	}
	rbacAuthorizeTotalCounter.WithLabelValues(result).Inc()
}

func ObserveRbacDeny(reason string) {
	if rbacAuthorizeDenyTotalCounter == nil {
		return
	}
	reason = strings.TrimSpace(strings.ToLower(reason))
	if reason == "" {
		reason = "unknown"
	}
	rbacAuthorizeDenyTotalCounter.WithLabelValues(reason).Inc()
}

func ObserveRbacCacheHit(layer string) {
	if rbacCacheHitTotalCounter == nil {
		return
	}
	layer = strings.TrimSpace(strings.ToLower(layer))
	if layer == "" {
		layer = "unknown"
	}
	rbacCacheHitTotalCounter.WithLabelValues(layer).Inc()
}

func ObserveRbacCacheMiss(layer string) {
	if rbacCacheMissTotalCounter == nil {
		return
	}
	layer = strings.TrimSpace(strings.ToLower(layer))
	if layer == "" {
		layer = "unknown"
	}
	rbacCacheMissTotalCounter.WithLabelValues(layer).Inc()
}

func ObserveRbacInvalidation(kind, result string) {
	if rbacInvalidationTotalCounter == nil {
		return
	}
	kind = strings.TrimSpace(strings.ToLower(kind))
	if kind == "" {
		kind = "unknown"
	}
	result = strings.TrimSpace(strings.ToLower(result))
	if result == "" {
		result = "unknown"
	}
	rbacInvalidationTotalCounter.WithLabelValues(kind, result).Inc()
}

func ObserveRbacSync(result string) {
	if rbacSyncTotalCounter == nil {
		return
	}
	result = strings.TrimSpace(strings.ToLower(result))
	if result == "" {
		result = "unknown"
	}
	rbacSyncTotalCounter.WithLabelValues(result).Inc()
}
