package svc_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"controlplane/internal/cacheengine"
	l2Cache "controlplane/internal/cacheengine/l2_cache"
	coreSvcImpl "controlplane/internal/zone/service"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

type mockL2Cache struct {
	cacheengine.L2Cache
	data map[string]interface{}
}

func (m *mockL2Cache) Set(ctx context.Context, key string, data interface{}, version int64, ttl time.Duration) error {
	m.data[key] = data
	return nil
}

func (m *mockL2Cache) Get(ctx context.Context, key string) (payload []byte, version int64, exists bool, err error) {
	val, ok := m.data[key]
	if !ok {
		return nil, 0, false, nil
	}
	bytes, _ := json.Marshal(val)
	return bytes, 1, true, nil
}

func (m *mockL2Cache) Delete(ctx context.Context, key string) error {
	delete(m.data, key)
	return nil
}

func (m *mockL2Cache) Client() *goredis.Client {
	return nil
}

func TestReportBackpressure_And_Throttling(t *testing.T) {
	// Create real L1 Cache
	l1 := cacheengine.NewL1Cache()
	registry := cacheengine.NewCacheRegistry(l1)

	// Create mock L2 Cache
	mockL2 := &mockL2Cache{data: make(map[string]interface{})}
	registry.L2 = mockL2

	// Create dummy Fanout (Publish will return 0, nil without calling Redis)
	registry.Fanout = &cacheengine.RedisFanout{}

	// Instantiate BackpressureService with registry
	svc := coreSvcImpl.NewBackpressureService(registry)

	zoneID, _ := uuid.NewV7()

	// 1. Report congestion (congested = true, epoch = 100, rate = 0.95)
	err := svc.ReportBackpressure(context.Background(), zoneID.String(), 6000, 600, true, 100, 0.95)
	if err != nil {
		t.Fatalf("failed to report backpressure: %v", err)
	}

	// 2. Report healthy again (congested = false, epoch = 101, rate = 0.0)
	err = svc.ReportBackpressure(context.Background(), zoneID.String(), 0, 0, false, 101, 0.0)
	if err != nil {
		t.Fatalf("failed to report backpressure: %v", err)
	}
}

func TestReportBackpressure_CAS(t *testing.T) {
	// 1. Start Miniredis to test real Redis L2 Client calls & CAS Lua Script
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer mr.Close()

	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	// Create real L1 Cache & L2 Cache backed by Miniredis
	l1 := cacheengine.NewL1Cache()
	registry := cacheengine.NewCacheRegistry(l1)
	registry.L2 = l2Cache.NewL2Cache(rdb)

	// Create dummy Fanout
	registry.Fanout = &cacheengine.RedisFanout{}

	svc := coreSvcImpl.NewBackpressureService(registry)
	zoneID, _ := uuid.NewV7()

	// Register "zone_backpressure" loader statically in registry for testing
	cacheengine.Register(registry, "zone_backpressure", 30*time.Second, func(ctx context.Context, param string) (map[string]interface{}, error) {
		key := "zone_backpressure:" + param
		payload, _, exists, err := registry.L2.Get(ctx, key)
		if err != nil {
			return nil, err
		}
		if !exists {
			return map[string]interface{}{
				"zone_id":   param,
				"congested": false,
			}, nil
		}
		var result map[string]interface{}
		if err := json.Unmarshal(payload, &result); err != nil {
			return nil, err
		}
		return result, nil
	})

	// 1. Report with epoch 100
	err = svc.ReportBackpressure(context.Background(), zoneID.String(), 6000, 600, true, 100, 0.95)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Read state back and verify it succeeded
	val, err := registry.GetOrLoad(context.Background(), "zone_backpressure", zoneID.String())
	if err != nil {
		t.Fatalf("failed to GetOrLoad: %v", err)
	}
	bp := val.(map[string]interface{})
	if !bp["congested"].(bool) || bp["epoch"].(float64) != 100 || bp["congestion_rate"].(float64) != 0.95 {
		t.Fatalf("expected epoch 100 and congestion_rate 0.95, got %v", bp)
	}

	// 2. Report with stale epoch 99 (must be rejected/ignored by CAS)
	err = svc.ReportBackpressure(context.Background(), zoneID.String(), 7000, 700, true, 99, 0.98)
	if err != nil {
		t.Fatalf("expected no error on stale report, got %v", err)
	}

	// Clear L1 to force reading from L2 (Miniredis)
	registry.L1.Delete("zone_backpressure:" + zoneID.String())

	val, err = registry.GetOrLoad(context.Background(), "zone_backpressure", zoneID.String())
	if err != nil {
		t.Fatalf("failed to GetOrLoad: %v", err)
	}
	bp = val.(map[string]interface{})
	if bp["epoch"].(float64) != 100 || bp["congestion_rate"].(float64) != 0.95 {
		t.Fatalf("expected stale write to be rejected. Got epoch %v, rate %v", bp["epoch"], bp["congestion_rate"])
	}

	// 3. Report with epoch 101 (must succeed)
	err = svc.ReportBackpressure(context.Background(), zoneID.String(), 1000, 100, true, 101, 0.70)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	registry.L1.Delete("zone_backpressure:" + zoneID.String())

	val, err = registry.GetOrLoad(context.Background(), "zone_backpressure", zoneID.String())
	if err != nil {
		t.Fatalf("failed to GetOrLoad: %v", err)
	}
	bp = val.(map[string]interface{})
	if bp["epoch"].(float64) != 101 || bp["congestion_rate"].(float64) != 0.70 {
		t.Fatalf("expected new write to succeed. Got epoch %v, rate %v", bp["epoch"], bp["congestion_rate"])
	}
}
