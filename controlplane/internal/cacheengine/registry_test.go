package cacheengine

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"controlplane/internal/cacheengine/fanout_cache"
	"controlplane/internal/cacheengine/l1_cache"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// TestRegistryGenericRegister kiểm thử cơ chế Go Generics Register[T] tự sinh Factory
func TestRegistryGenericRegister(t *testing.T) {
	cache := l1_cache.NewShardedCache()
	defer cache.Close()
	registry := NewCacheRegistry(cache)

	type CustomData struct {
		Name string
		Age  int
	}

	// Đăng ký sử dụng generic function
	Register(registry, "custom", time.Hour, func(ctx context.Context, param string) (CustomData, error) {
		return CustomData{Name: param, Age: 25}, nil
	})

	// 1. Kiểm tra loader được đăng ký đúng
	loader := registry.GetLoader("custom")
	if loader == nil {
		t.Fatal("expected loader to be registered")
	}

	// 2. Kiểm tra Factory tự sinh kiểu dữ liệu chính xác (*CustomData)
	ptr := loader.Factory()
	if _, ok := ptr.(*CustomData); !ok {
		t.Fatalf("expected factory to return pointer to CustomData (*CustomData), got %T", ptr)
	}

	// 3. Test GetOrLoad qua registry trả về đúng kiểu dữ liệu CustomData gốc (không bọc con trỏ)
	val, err := registry.GetOrLoad(context.Background(), "custom", "John")
	if err != nil {
		t.Fatalf("unexpected registry load error: %v", err)
	}

	data, ok := val.(CustomData)
	if !ok || data.Name != "John" || data.Age != 25 {
		t.Fatalf("expected CustomData{John, 25}, got %v (type %T)", val, val)
	}
}

// TestRegistryGetOrLoadError kiểm thử luồng lỗi nạp từ loader
func TestRegistryGetOrLoadError(t *testing.T) {
	cache := l1_cache.NewShardedCache()
	defer cache.Close()
	registry := NewCacheRegistry(cache)

	Register(registry, "err_ns", time.Hour, func(ctx context.Context, param string) (string, error) {
		return "", errors.New("database connection failure")
	})

	_, err := registry.GetOrLoad(context.Background(), "err_ns", "test")
	if err == nil || err.Error() != "database connection failure" {
		t.Fatalf("expected database connection failure error, got %v", err)
	}
}

// TestRedisFanoutAtomicIncr kiểm thử luồng Redis Fanout đồng bộ version nguyên tử và xoá/cập nhật cache
func TestRedisFanoutAtomicIncr(t *testing.T) {
	// 1. Khởi tạo Mock Redis bằng Miniredis
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer mr.Close()

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	cache := l1_cache.NewShardedCache()
	defer cache.Close()
	registry := NewCacheRegistry(cache)

	// Đăng ký Loader cho namespace "zone_catalog"
	Register(registry, "zone_catalog", time.Hour, func(ctx context.Context, param string) (string, error) {
		return "loaded_from_db", nil
	})

	fanoutBus := fanout_cache.NewRedisFanout(rdb, "test_channel")
	registry.Fanout = fanoutBus

	// Khởi chạy Subscription Loop chạy ngầm
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = registry.StartSubscribe(ctx)
	}()

	// Đợi subscription kết nối thành công lên Redis channel
	time.Sleep(50 * time.Millisecond)

	// 2. Kiểm thử chống tràn bộ nhớ (OOM Prevention)
	// Trực tiếp phát update một key chưa tồn tại trong L1 -> Không được phép tự động lưu vào RAM
	payload, _ := json.Marshal("new_data")
	ver1, err := fanoutBus.Publish(context.Background(), "zone_catalog:dynamic", payload)
	if err != nil {
		t.Fatalf("unexpected publish error: %v", err)
	}
	if ver1 != 1 {
		t.Fatalf("expected first version to be 1, got %d", ver1)
	}

	time.Sleep(50 * time.Millisecond) // Đợi tin nhắn lan truyền

	_, exists := registry.L1.Get("zone_catalog:dynamic")
	if exists {
		t.Fatal("expected key to NOT be added to L1 (OOM Prevention failed)")
	}

	// 3. Thiết lập sẵn key trong RAM L1 cục bộ để giả lập key đang active
	registry.L1.Set("zone_catalog:dynamic", &L1Envelope{
		Key:     "zone_catalog:dynamic",
		Version: 0,
		Value:   "old_data",
	}, time.Hour)

	// 4. Phát tán tin nhắn cập nhật (Update) -> Phải cập nhật dữ liệu và version mới
	ver2, err := fanoutBus.Publish(context.Background(), "zone_catalog:dynamic", payload)
	if err != nil {
		t.Fatalf("unexpected publish error: %v", err)
	}
	if ver2 != 2 {
		t.Fatalf("expected version to be incremented to 2, got %d", ver2)
	}

	time.Sleep(50 * time.Millisecond) // Đợi tin nhắn lan truyền

	val, exists := registry.L1.Get("zone_catalog:dynamic")
	if !exists {
		t.Fatal("expected key to exist in L1")
	}
	env := val.(*L1Envelope)
	if env.Version != 2 || env.Value.(string) != "new_data" {
		t.Fatalf("expected version 2 and value 'new_data', got version %d and value %v", env.Version, env.Value)
	}

	// 5. Kiểm thử stale write check
	// Gửi tin nhắn update với version cũ hơn version hiện tại trên RAM -> Phải bỏ qua
	// Giả lập bằng cách gọi lại tin nhắn cũ hoặc publish thủ công tin nhắn có version 1
	staleMsg := fanout_cache.FanoutMessage{
		Key:     "zone_catalog:dynamic",
		Version: 1, // Version 1 < Version 2 hiện tại trên RAM
		Payload: payload,
	}
	staleBytes, _ := json.Marshal(staleMsg)
	rdb.Publish(context.Background(), "test_channel", staleBytes)

	time.Sleep(50 * time.Millisecond)

	val, _ = registry.L1.Get("zone_catalog:dynamic")
	env = val.(*L1Envelope)
	if env.Version != 2 {
		t.Fatalf("expected version to remain 2, but was overwritten to version %d", env.Version)
	}

	// 6. Kiểm thử lệnh xóa (Delete Invalidation)
	// Phát sự kiện xóa (payload = nil) -> Phải xóa sạch key khỏi L1
	_, err = fanoutBus.Publish(context.Background(), "zone_catalog:dynamic", nil)
	if err != nil {
		t.Fatalf("unexpected publish error on delete: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	_, exists = registry.L1.Get("zone_catalog:dynamic")
	if exists {
		t.Fatal("expected key to be deleted from L1 cache")
	}
}
