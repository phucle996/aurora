package l1_cache

import (
	"sync"
	"testing"
	"time"
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
	cache := NewShardedCache()
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
	// Tạo cache và khởi tạo thủ công sweeper chu kỳ siêu ngắn để chạy test nhanh
	c := &shardedCache{
		stopSweeperSig: make(chan struct{}),
	}
	shards := make([]*cacheShard, shardCount)
	for i := 0; i < shardCount; i++ {
		shards[i] = &cacheShard{
			items: make(map[string]cacheItem),
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

func BenchmarkCacheGet(b *testing.B) {
	cache := NewShardedCache()
	defer cache.Close()
	cache.Set("key1", "value1", time.Hour)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = cache.Get("key1")
		}
	})
}

func BenchmarkCacheSet(b *testing.B) {
	cache := NewShardedCache()
	defer cache.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			cache.Set("key1", "value1", time.Hour)
		}
	})
}
