package l1_cache

import (
	"math/rand/v2" // Thư viện rand/v2 mới của Go 1.22+ (thread-safe, hiệu năng cao)
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// Cache định nghĩa interface cho L1 Cache Engine nâng cấp các tính năng SRE
type Cache interface {
	Get(key string) (interface{}, bool)
	Set(key string, val interface{}, ttl time.Duration)
	Delete(key string) bool
	Flush() // Xóa sạch toàn bộ dữ liệu trong RAM L1 (Dùng khi Redis Reconnect)
	Close() // Dừng Active Sweeper chạy ngầm (Dùng khi Graceful Shutdown)
	GetOrLoad(key string, ttl time.Duration, loadFn func() (interface{}, error)) (interface{}, error)
}

type cacheItem struct {
	val       interface{}
	expiresAt time.Time
}

func (item *cacheItem) isExpired() bool {
	return time.Now().After(item.expiresAt)
}

type cacheShard struct {
	mu          sync.RWMutex
	items       map[string]cacheItem
	deletions   map[string]time.Time
	activeLoads map[string]int
	sf          singleflight.Group
}

type shardedCache struct {
	shards         []*cacheShard
	mask           uint32
	stopSweeperSig chan struct{} // Tín hiệu dừng Active Sweeper
	wg             sync.WaitGroup
}

const shardCount = 32

// NewShardedCache khởi tạo một in-memory L1 cache phân mảnh để tránh lock contention,
// đồng thời kích hoạt luồng Active Sweeper dọn dẹp RAM định kỳ.
func NewShardedCache() Cache {
	c := &shardedCache{
		shards:         nil,
		mask:           0,
		stopSweeperSig: make(chan struct{}),
	}

	shards := make([]*cacheShard, shardCount)
	for i := 0; i < shardCount; i++ {
		shards[i] = &cacheShard{
			items:       make(map[string]cacheItem),
			deletions:   make(map[string]time.Time),
			activeLoads: make(map[string]int),
		}
	}
	c.shards = shards
	c.mask = uint32(shardCount - 1)

	// Khởi chạy Active Sweeper chạy ngầm mỗi 1 phút để tránh rò rỉ RAM (OOM Prevention)
	c.wg.Add(1)
	go c.startActiveSweeper(1 * time.Minute)

	return c
}

// startActiveSweeper định kỳ dọn dẹp các cache item đã quá hạn trên toàn bộ các shards
func (c *shardedCache) startActiveSweeper(interval time.Duration) {
	defer c.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopSweeperSig:
			return // Nhận tín hiệu dừng từ Close() -> thoát goroutine
		case <-ticker.C:
			now := time.Now()
			// Quét lần lượt từng shard để tránh khóa toàn bộ cache cùng lúc (giảm thiểu lock contention)
			for i := 0; i < len(c.shards); i++ {
				shard := c.shards[i]
				shard.mu.Lock()
				for k, item := range shard.items {
					if !item.expiresAt.IsZero() && now.After(item.expiresAt) {
						if item.val != nil {
							if env, ok := item.val.(*L1Envelope); ok {
								if zeroable, ok := env.Value.(Zeroable); ok {
									zeroable.Zero()
								}
							}
						}
						delete(shard.items, k)
					}
				}
				shard.mu.Unlock()
			}
		}
	}
}

// Flush xóa sạch toàn bộ phần tử trong RAM L1. Dùng khi khôi phục kết nối Redis để đảm bảo nhất quán.
func (c *shardedCache) Flush() {
	for i := 0; i < len(c.shards); i++ {
		shard := c.shards[i]
		shard.mu.Lock()
		for _, item := range shard.items {
			if item.val != nil {
				if env, ok := item.val.(*L1Envelope); ok {
					if zeroable, ok := env.Value.(Zeroable); ok {
						zeroable.Zero()
					}
				}
			}
		}
		shard.items = make(map[string]cacheItem)
		shard.deletions = make(map[string]time.Time)
		shard.activeLoads = make(map[string]int)
		shard.mu.Unlock()
	}
}

// Close dừng luồng Active Sweeper chạy nền và đợi giải phóng tài nguyên êm đẹp (Graceful Shutdown)
func (c *shardedCache) Close() {
	close(c.stopSweeperSig)
	c.wg.Wait()
}

// getShardIndex dùng FNV-1a thủ công để xác định shard tương ứng với key mà không cấp phát bộ nhớ.
func (c *shardedCache) getShardIndex(key string) uint32 {
	const (
		offset32 = 2166136261
		prime32  = 16777619
	)
	hash := uint32(offset32)
	for i := 0; i < len(key); i++ {
		hash ^= uint32(key[i])
		hash *= prime32
	}
	return hash & c.mask
}

// applySkew tính toán lại TTL ngẫu nhiên lệch pha từ +- 10% TTL, thấp nhất 30s và cao nhất 100s.
func (c *shardedCache) applySkew(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return ttl
	}

	// Biên độ skew là 10% TTL
	skewRange := float64(ttl) * 0.1

	// Áp dụng giới hạn dưới 30s cho các TTL thực tế lớn hơn hoặc bằng 30s
	if ttl >= 30*time.Second {
		if skewRange < float64(30*time.Second) {
			skewRange = float64(30 * time.Second)
		}
	}
	// Khống chế biên độ tối đa là 100s
	if skewRange > float64(100*time.Second) {
		skewRange = float64(100 * time.Second)
	}

	// Sinh số thực ngẫu nhiên trong khoảng [-skewRange, skewRange]
	randomOffset := (rand.Float64()*2.0 - 1.0) * skewRange

	jittered := ttl + time.Duration(randomOffset)
	if jittered <= 0 {
		return ttl
	}
	return jittered
}

// Get đọc dữ liệu từ cache shard (hỗ trợ lazy eviction nếu dữ liệu hết hạn)
func (c *shardedCache) Get(key string) (interface{}, bool) {
	idx := c.getShardIndex(key)
	shard := c.shards[idx]

	shard.mu.RLock()
	item, ok := shard.items[key]
	shard.mu.RUnlock()

	if !ok {
		return nil, false
	}

	if item.isExpired() {
		// Lazy eviction
		shard.mu.Lock()
		// Double-check under write lock
		if item, ok = shard.items[key]; ok && item.isExpired() {
			delete(shard.items, key)
		}
		shard.mu.Unlock()
		return nil, false
	}

	return item.val, true
}

// Set ghi dữ liệu vào cache shard với TTL có áp dụng Jitter Skew ngẫu nhiên
func (c *shardedCache) Set(key string, val interface{}, ttl time.Duration) {
	idx := c.getShardIndex(key)
	shard := c.shards[idx]

	// Áp dụng skew ngẫu nhiên cho TTL
	jitteredTTL := c.applySkew(ttl)

	shard.mu.Lock()
	shard.items[key] = cacheItem{
		val:       val,
		expiresAt: time.Now().Add(jitteredTTL),
	}
	shard.mu.Unlock()
}

// Delete xóa một key cụ thể khỏi cache shard và trả về true nếu key tồn tại
func (c *shardedCache) Delete(key string) bool {
	idx := c.getShardIndex(key)
	shard := c.shards[idx]

	shard.mu.Lock()
	defer shard.mu.Unlock()

	// Chỉ lưu vết deletion tombstone nếu đang có DB load in-flight cho key này
	if activeCount := shard.activeLoads[key]; activeCount > 0 {
		shard.deletions[key] = time.Now()
	}
	shard.sf.Forget(key)

	if item, exists := shard.items[key]; exists {
		if item.val != nil {
			if env, ok := item.val.(*L1Envelope); ok {
				if zeroable, ok := env.Value.(Zeroable); ok {
					zeroable.Zero()
				}
			}
		}
		delete(shard.items, key)
		return true
	}
	return false
}

// GetOrLoad đọc dữ liệu từ cache, nếu miss sẽ dùng singleflight gọi loadFn để tải lại
func (c *shardedCache) GetOrLoad(key string, ttl time.Duration, loadFn func() (interface{}, error)) (interface{}, error) {
	// 1. Kiểm tra cache trước
	if val, ok := c.Get(key); ok {
		return val, nil
	}

	// 2. Cache miss -> Định tuyến shard và thực thi qua singleflight
	idx := c.getShardIndex(key)
	shard := c.shards[idx]
	startTime := time.Now()

	// Dùng singleflight để đảm bảo chỉ có tối đa 1 goroutine thực thi loadFn cho key này tại một thời điểm
	val, err, _ := shard.sf.Do(key, func() (interface{}, error) {
		// Đánh dấu bắt đầu load DB
		shard.mu.Lock()
		shard.activeLoads[key]++
		shard.mu.Unlock()

		// Đảm bảo tự động giảm activeLoads và dọn dẹp tombstone khi hoàn tất
		defer func() {
			shard.mu.Lock()
			shard.activeLoads[key]--
			if shard.activeLoads[key] <= 0 {
				delete(shard.activeLoads, key)
				delete(shard.deletions, key) // Tự động xóa tombstone ngay lập tức để giải phóng RAM!
			}
			shard.mu.Unlock()
		}()

		// Double check cache dưới read lock đề phòng goroutine khác vừa nạp thành công
		shard.mu.RLock()
		item, ok := shard.items[key]
		shard.mu.RUnlock()
		if ok && !item.isExpired() {
			return item.val, nil
		}

		// Gọi hàm nạp dữ liệu gốc
		res, err := loadFn()
		if err != nil {
			return nil, err
		}

		// Tính toán TTL ngẫu nhiên lệch pha
		jitteredTTL := c.applySkew(ttl)

		// Ghi đè vào cache nếu không bị delete/invalidate trong quá trình load
		shard.mu.Lock()
		defer shard.mu.Unlock()

		if deletedAt, exists := shard.deletions[key]; exists && deletedAt.After(startTime) {
			// SRE Warning: Phát hiện có lệnh Delete/Invalidate xảy ra TRONG KHI đang load DB.
			// Bỏ qua không ghi đè vào cache để tránh stale write (magic/stale cache record).
			return res, nil
		}

		shard.items[key] = cacheItem{
			val:       res,
			expiresAt: time.Now().Add(jitteredTTL),
		}

		return res, nil
	})

	return val, err
}
