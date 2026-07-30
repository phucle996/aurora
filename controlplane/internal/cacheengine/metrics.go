// ============================================================================
// 📂 MODULE: controlplane/internal/cacheengine/metrics.go
//            Đo Lường L1 Cache Operations (OTel Metrics)
// ============================================================================
// Ghi nhận telemetry cho mọi thao tác Get/Set/Delete/Flush/GetOrLoad trên L1 Cache.
// Recorder được inject từ app; cache không tự tạo meter hay global state.
// ============================================================================

package cacheengine

import (
	"context"
	"strings"
	"time"

	"controlplane/internal/observability"
)

// getNamespace trích xuất phần tiền tố (namespace) từ cache key để gom nhóm metrics.
// Sử dụng strings.IndexByte để đảm bảo zero allocation trên hot path.
func getNamespace(key string, namespaceRegistered func(string) bool) string {
	namespace := key
	if idx := strings.IndexByte(key, ':'); idx != -1 {
		namespace = key[:idx]
	}
	if namespaceRegistered != nil && namespaceRegistered(namespace) {
		return namespace
	}
	// Keys and prefixes may be caller-controlled. Only namespaces registered
	// during cache bootstrap are allowed to become metric labels.
	return "unknown"
}

// telemetryL1Cache là decorator bao bọc L1 Cache để tự động ghi nhận telemetry.
type telemetryL1Cache struct {
	raw                 Cache
	metrics             observability.CacheRecorder
	namespaceRegistered func(string) bool
}

func (w *telemetryL1Cache) Get(key string) (interface{}, bool) {
	val, ok := w.raw.Get(key)
	cacheName := getNamespace(key, w.namespaceRegistered)
	if ok {
		w.metrics.ObserveCache(context.Background(), "l1", cacheName, "get", "hit")
	} else {
		w.metrics.ObserveCache(context.Background(), "l1", cacheName, "get", "miss")
	}
	return val, ok
}

func (w *telemetryL1Cache) Set(key string, val interface{}, ttl time.Duration) {
	w.raw.Set(key, val, ttl)
	cacheName := getNamespace(key, w.namespaceRegistered)
	w.metrics.ObserveCache(context.Background(), "l1", cacheName, "set", "success")
}

func (w *telemetryL1Cache) Delete(key string) bool {
	ok := w.raw.Delete(key)
	cacheName := getNamespace(key, w.namespaceRegistered)
	if ok {
		w.metrics.ObserveCache(context.Background(), "l1", cacheName, "delete", "success")
	} else {
		w.metrics.ObserveCache(context.Background(), "l1", cacheName, "delete", "miss")
	}
	return ok
}

func (w *telemetryL1Cache) Flush() {
	w.raw.Flush()
	w.metrics.ObserveCache(context.Background(), "l1", "all", "flush", "success")
}

func (w *telemetryL1Cache) Close() {
	w.raw.Close()
}

func (w *telemetryL1Cache) GetOrLoad(key string, ttl time.Duration, loadFn func() (interface{}, error)) (interface{}, error) {
	var wasMiss bool
	cacheName := getNamespace(key, w.namespaceRegistered)
	val, err := w.raw.GetOrLoad(key, ttl, func() (interface{}, error) {
		wasMiss = true
		return loadFn()
	})

	if err != nil {
		w.metrics.ObserveCache(context.Background(), "l1", cacheName, "get_or_load", "error")
		return nil, err
	}

	if wasMiss {
		w.metrics.ObserveCache(context.Background(), "l1", cacheName, "get_or_load", "miss")
	} else {
		w.metrics.ObserveCache(context.Background(), "l1", cacheName, "get_or_load", "hit")
	}
	return val, nil
}
