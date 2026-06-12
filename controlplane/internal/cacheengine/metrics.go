package cacheengine

import (
	"strings"
	"sync"
	"time"

	"controlplane/internal/observability"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	registerOnce sync.Once

	// l1CacheOperationsCounter đếm tổng số lần thao tác với L1 Cache.
	// FQN: aurora_controlplane_cache_l1_operations_total
	l1CacheOperationsCounter *prometheus.CounterVec
)

// RegisterCacheMetrics đăng ký metrics của L1 Cache vào Prometheus Registry.
func RegisterCacheMetrics(registry *prometheus.Registry, namespace string) error {
	var registerErr error
	registerOnce.Do(func() {
		l1CacheOperationsCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "cache",
			Name:      "l1_operations_total",
			Help:      "Total operations on L1 in-memory cache, partitioned by operation name, cache name (namespace) and outcome.",
		}, []string{"operation", "cache_name", "outcome"})

		if err := registry.Register(l1CacheOperationsCounter); err != nil {
			registerErr = err
		}
	})
	return registerErr
}

func init() {
	// Tự đăng ký module metrics vào callback chain của observability.
	observability.RegisterModuleMetrics(RegisterCacheMetrics)
}

func recordL1Operation(operation, cacheName, outcome string) {
	if l1CacheOperationsCounter != nil {
		l1CacheOperationsCounter.WithLabelValues(operation, cacheName, outcome).Inc()
	}
}

// getNamespace trích xuất phần tiền tố (namespace) từ cache key để gom nhóm metrics.
// Sử dụng strings.IndexByte để đảm bảo zero allocation trên hot path.
func getNamespace(key string) string {
	if idx := strings.IndexByte(key, ':'); idx != -1 {
		return key[:idx]
	}
	return key
}

// telemetryL1Cache là decorator bao bọc L1 Cache để tự động ghi nhận telemetry.
type telemetryL1Cache struct {
	raw Cache
}

func (w *telemetryL1Cache) Get(key string) (interface{}, bool) {
	val, ok := w.raw.Get(key)
	cacheName := getNamespace(key)
	if ok {
		recordL1Operation("get", cacheName, "hit")
	} else {
		recordL1Operation("get", cacheName, "miss")
	}
	return val, ok
}

func (w *telemetryL1Cache) Set(key string, val interface{}, ttl time.Duration) {
	w.raw.Set(key, val, ttl)
	cacheName := getNamespace(key)
	recordL1Operation("set", cacheName, "success")
}

func (w *telemetryL1Cache) Delete(key string) bool {
	ok := w.raw.Delete(key)
	cacheName := getNamespace(key)
	if ok {
		recordL1Operation("delete", cacheName, "success")
	} else {
		recordL1Operation("delete", cacheName, "miss")
	}
	return ok
}

func (w *telemetryL1Cache) Flush() {
	w.raw.Flush()
	recordL1Operation("flush", "all", "success")
}

func (w *telemetryL1Cache) Close() {
	w.raw.Close()
}

func (w *telemetryL1Cache) GetOrLoad(key string, ttl time.Duration, loadFn func() (interface{}, error)) (interface{}, error) {
	var wasMiss bool
	cacheName := getNamespace(key)
	val, err := w.raw.GetOrLoad(key, ttl, func() (interface{}, error) {
		wasMiss = true
		return loadFn()
	})

	if err != nil {
		recordL1Operation("get_or_load", cacheName, "error")
		return nil, err
	}

	if wasMiss {
		recordL1Operation("get_or_load", cacheName, "miss")
	} else {
		recordL1Operation("get_or_load", cacheName, "hit")
	}
	return val, nil
}
