// ============================================================================
// 📂 MODULE: controlplane/internal/cacheengine/metrics.go
//            Đo Lường L1 Cache Operations (OTel Metrics)
// ============================================================================
// Ghi nhận telemetry cho mọi thao tác Get/Set/Delete/Flush/GetOrLoad trên L1 Cache.
// Sử dụng native OTel Counter, lazy init qua sync.Once.
// ============================================================================

package cacheengine

import (
	"context"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	metricsOnce sync.Once

	// l1CacheOperationsCounter đếm tổng số thao tác với L1 Cache.
	l1CacheOperationsCounter metric.Int64Counter
)

// ensureCacheMetrics khởi tạo OTel instrument cho cache metrics.
func ensureCacheMetrics() {
	metricsOnce.Do(func() {
		meter := otel.Meter("aurora-controlplane.cache")
		l1CacheOperationsCounter, _ = meter.Int64Counter(
			"aurora_controlplane_cache_l1_operations_total",
			metric.WithDescription("Total operations on L1 in-memory cache, partitioned by operation name, cache name and outcome."),
		)
	})
}

// recordL1Operation ghi nhận một thao tác L1 Cache vào OTel Counter.
func recordL1Operation(operation, cacheName, outcome string) {
	ensureCacheMetrics()
	if l1CacheOperationsCounter != nil {
		l1CacheOperationsCounter.Add(context.Background(), 1,
			metric.WithAttributes(
				attribute.String("operation", operation),
				attribute.String("cache_name", cacheName),
				attribute.String("outcome", outcome),
			),
		)
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
