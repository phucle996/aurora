package cacheengine

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// TestCacheBasicOps kiểm thử các thao tác cơ bản: Get, Set, Delete
func TestCacheBasicOps(t *testing.T) {
	cache := NewShardedCache()
	defer cache.Close()

	// 1. Set & Get
	cache.Set("key1", "value1", 10*time.Second)
	val, ok := cache.Get("key1")
	if !ok || val.(string) != "value1" {
		t.Fatalf("expected value1, got %v", val)
	}

	// 2. Delete
	deleted := cache.Delete("key1")
	if !deleted {
		t.Fatal("expected key1 to be deleted")
	}

	_, ok = cache.Get("key1")
	if ok {
		t.Fatal("expected key1 to be evicted after Delete")
	}
}

// TestCacheLazyEviction kiểm thử cơ chế tự xóa khi hết hạn của Get (Lazy Eviction)
func TestCacheLazyEviction(t *testing.T) {
	// Khởi tạo cache tắt Jitter để kiểm tra TTL chính xác
	cache := NewShardedCache(WithJitter(0.0))
	defer cache.Close()

	cache.Set("key1", "value1", 10*time.Millisecond)

	// Đợi quá TTL
	time.Sleep(15 * time.Millisecond)

	_, ok := cache.Get("key1")
	if ok {
		t.Fatal("expected key1 to be lazy evicted after expiration")
	}
}

// TestCacheActiveSweeper kiểm thử luồng quét ngầm giải phóng các key hết hạn (Active Eviction)
func TestCacheActiveSweeper(t *testing.T) {
	// Tạo cache với Jitter = 0.0 và khởi tạo thủ công sweeper chu kỳ siêu ngắn để chạy test nhanh
	c := &shardedCache{
		stopSweeperSig: make(chan struct{}),
		jitterFactor:   0.0,
	}
	shards := make([]*cacheShard, shardCount)
	for i := 0; i < shardCount; i++ {
		shards[i] = &cacheShard{
			items: make(map[string]*cacheItem),
		}
	}
	c.shards = shards
	c.mask = uint32(shardCount - 1)

	// Khởi chạy active sweeper chu kỳ 10ms
	c.wg.Add(1)
	go c.startActiveSweeper(10 * time.Millisecond)
	defer c.Close()

	c.Set("temp1", "val1", 5*time.Millisecond)
	c.Set("temp2", "val2", 500*time.Millisecond) // Key này sẽ KHÔNG bị dọn

	// Đợi sweeper quét qua (sau 20ms)
	time.Sleep(30 * time.Millisecond)

	// temp1 phải biến mất khỏi map hoàn toàn (không cần gọi qua Get)
	idx := c.getShardIndex("temp1")
	c.shards[idx].mu.RLock()
	_, exists1 := c.shards[idx].items["temp1"]
	c.shards[idx].mu.RUnlock()
	if exists1 {
		t.Fatal("expected temp1 to be active-evicted by sweeper")
	}

	// temp2 vẫn phải tồn tại vì chưa quá hạn
	idx2 := c.getShardIndex("temp2")
	c.shards[idx2].mu.RLock()
	_, exists2 := c.shards[idx2].items["temp2"]
	c.shards[idx2].mu.RUnlock()
	if !exists2 {
		t.Fatal("expected temp2 to remain in cache")
	}
}

// TestCacheFlush kiểm thử xóa sạch RAM L1
func TestCacheFlush(t *testing.T) {
	cache := NewShardedCache()
	defer cache.Close()

	cache.Set("key1", "val1", time.Hour)
	cache.Set("key2", "val2", time.Hour)

	cache.Flush()

	_, ok1 := cache.Get("key1")
	_, ok2 := cache.Get("key2")
	if ok1 || ok2 {
		t.Fatal("expected all keys to be cleared after Flush")
	}
}

// TestCacheGetOrLoadSingleflight kiểm thử concurrency control trên cache miss
func TestCacheGetOrLoadSingleflight(t *testing.T) {
	cache := NewShardedCache()
	defer cache.Close()

	var loadCount int
	var mu sync.Mutex
	loadFn := func() (interface{}, error) {
		mu.Lock()
		loadCount++
		mu.Unlock()
		time.Sleep(50 * time.Millisecond) // Giả lập DB trễ
		return "db_result", nil
	}

	// Chạy song song 10 goroutines cùng đọc 1 key trống
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			val, err := cache.GetOrLoad("key_miss", 10*time.Second, loadFn)
			if err != nil || val.(string) != "db_result" {
				t.Errorf("unexpected load error or value: %v", err)
			}
		}()
	}
	wg.Wait()

	// Chỉ được phép gọi hàm nạp dữ liệu từ DB đúng 1 lần (Singleflight bảo vệ)
	if loadCount != 1 {
		t.Fatalf("expected DB loader to run exactly once under concurrency, ran %d times", loadCount)
	}
}

// TestRegistryGenericRegister kiểm thử cơ chế Go Generics Register[T] tự sinh Factory
func TestRegistryGenericRegister(t *testing.T) {
	cache := NewShardedCache()
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
	cache := NewShardedCache()
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

	cache := NewShardedCache()
	defer cache.Close()
	registry := NewCacheRegistry(cache)

	// Đăng ký Loader cho namespace "zone_catalog"
	Register(registry, "zone_catalog", time.Hour, func(ctx context.Context, param string) (string, error) {
		return "loaded_from_db", nil
	})

	fanoutBus := NewRedisFanout(rdb, "test_channel", registry)

	// Khởi chạy Subscription Loop chạy ngầm
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = fanoutBus.StartSubscribe(ctx)
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

	_, exists := registry.GetLocalRaw("zone_catalog:dynamic")
	if exists {
		t.Fatal("expected key to NOT be added to L1 (OOM Prevention failed)")
	}

	// 3. Thiết lập sẵn key trong RAM L1 cục bộ để giả lập key đang active
	registry.SetLocalRaw("zone_catalog:dynamic", &CacheEnvelope{
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

	val, exists := registry.GetLocalRaw("zone_catalog:dynamic")
	if !exists {
		t.Fatal("expected key to exist in L1")
	}
	env := val.(*CacheEnvelope)
	if env.Version != 2 || env.Value.(string) != "new_data" {
		t.Fatalf("expected version 2 and value 'new_data', got version %d and value %v", env.Version, env.Value)
	}

	// 5. Kiểm thử stale write check
	// Gửi tin nhắn update với version cũ hơn version hiện tại trên RAM -> Phải bỏ qua
	// Giả lập bằng cách gọi lại tin nhắn cũ hoặc publish thủ công tin nhắn có version 1
	staleMsg := FanoutMessage{
		Key:     "zone_catalog:dynamic",
		Version: 1, // Version 1 < Version 2 hiện tại trên RAM
		Payload: payload,
	}
	staleBytes, _ := json.Marshal(staleMsg)
	rdb.Publish(context.Background(), "test_channel", staleBytes)

	time.Sleep(50 * time.Millisecond)

	val, _ = registry.GetLocalRaw("zone_catalog:dynamic")
	env = val.(*CacheEnvelope)
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

	_, exists = registry.GetLocalRaw("zone_catalog:dynamic")
	if exists {
		t.Fatal("expected key to be deleted from L1 cache")
	}
}
