package cache_test

import (
	"context"
	"testing"
	"time"

	iamCache "controlplane/internal/iam/cache"
	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func TestAdminAPIKeyCachePubSubInvalidation(t *testing.T) {
	redisServer := miniredis.RunT(t)
	rds := goredis.NewClient(&goredis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = rds.Close() })

	// Tạo 2 client đại diện cho 2 Pod HA khác nhau
	cachePod1 := iamCache.NewAdminAPIKeyCache(rds)
	cachePod2 := iamCache.NewAdminAPIKeyCache(rds)

	// Mock active key trên Pod 2
	key := iamEntity.AdminAPIKey{
		KeyHash:   "test-hash-key",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	cachePod2.SetActiveAPIKey(key, time.Minute)

	// Kiểm tra xem Pod 2 đã cache active key chưa
	_, ok := cachePod2.GetActiveAPIKey(time.Now().UTC())
	if !ok {
		t.Fatal("expected Pod 2 to cache the active API key")
	}

	// Đăng ký lắng nghe invalidation trên Pod 2
	invalidatedChan := make(chan bool, 1)
	cachePod2.SubscribeInvalidation(context.Background(), func() {
		cachePod2.InvalidateActiveAPIKey()
		invalidatedChan <- true
	})

	// Chờ một chút để Pub/Sub subscription sẵn sàng
	time.Sleep(10 * time.Millisecond)

	// Kích hoạt Invalidation từ Pod 1 (khi xoay khóa thành công)
	if err := cachePod1.PublishInvalidation(context.Background()); err != nil {
		t.Fatalf("failed to publish invalidation: %v", err)
	}

	// Chờ sự kiện lan truyền
	select {
	case <-invalidatedChan:
		// Thành công
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for cache invalidation event")
	}

	// Kiểm tra xem RAM cache của Pod 2 đã bị xóa sạch chưa
	_, okAfter := cachePod2.GetActiveAPIKey(time.Now().UTC())
	if okAfter {
		t.Fatal("expected Pod 2 cached active API key to be cleared after invalidation event")
	}
}
