package l2_cache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// TestL2CacheOps kiểm thử các thao tác Get và Delete của L2Cache.
func TestL2CacheOps(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer mr.Close()

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	ctx := context.Background()
	l2Cache := NewL2Cache(rdb)

	key := "rbac_role:admin"

	// 1. Kiểm thử Get khi chưa có dữ liệu (Cache Miss) -> Phải trả về exists = false và err = nil
	payload, version, exists, err := l2Cache.Get(ctx, key)
	if err != nil {
		t.Fatalf("unexpected error on cache miss: %v", err)
	}
	if exists {
		t.Fatal("expected cache item to not exist")
	}
	if len(payload) > 0 || version != 0 {
		t.Fatalf("expected empty payload and version 0, got payload=%v version=%d", payload, version)
	}

	// 2. Thiết lập dữ liệu L2 thủ công để giả lập việc ghi của Caller
	dataKey := "{" + key + "}:data"
	versionKey := "{" + key + "}:version"
	rdb.Set(ctx, dataKey, `{"permissions":["read","write"]}`, 10*time.Second)
	rdb.Set(ctx, versionKey, "1717960000000000000", 10*time.Second)

	// 3. Kiểm thử Get khi đã có dữ liệu (Cache Hit) -> Trả về đúng payload và version
	payload, version, exists, err = l2Cache.Get(ctx, key)
	if err != nil {
		t.Fatalf("unexpected error on cache hit: %v", err)
	}
	if !exists {
		t.Fatal("expected cache item to exist")
	}
	if string(payload) != `{"permissions":["read","write"]}` || version != 1717960000000000000 {
		t.Fatalf("unexpected payload or version, got payload=%s version=%d", payload, version)
	}

	// 4. Kiểm thử Delete -> Phải xóa cả dataKey và versionKey khỏi Redis
	err = l2Cache.Delete(ctx, key)
	if err != nil {
		t.Fatalf("unexpected error on delete: %v", err)
	}

	// Xác nhận phím dữ liệu biến mất trên Redis
	dataExists, _ := rdb.Exists(ctx, dataKey).Result()
	versionExists, _ := rdb.Exists(ctx, versionKey).Result()
	if dataExists > 0 || versionExists > 0 {
		t.Fatal("expected L2 keys to be deleted from Redis")
	}

	// Xác nhận Get trả về exists = false
	_, _, exists, _ = l2Cache.Get(ctx, key)
	if exists {
		t.Fatal("expected cache item to be missing after deletion")
	}
}
