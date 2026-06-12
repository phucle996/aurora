package fanout_cache

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

func getTestRedisAddr() string {
	if addr := strings.TrimSpace(os.Getenv("IAM_TEST_REDIS_ADDR")); addr != "" {
		return addr
	}
	return "127.0.0.1:16380"
}

func TestFanoutPublish(t *testing.T) {
	addr := getTestRedisAddr()
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()

	ctx := context.Background()
	// Kiểm tra kết nối trước
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("skipping test: Redis not reachable at %s: %v", addr, err)
	}

	fanout := NewRedisFanout(rdb, "sync_channel")

	version, err := fanout.Publish(ctx, "rbac_role:admin", []byte(`{"permissions":["read"]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if version <= 0 {
		t.Fatalf("expected positive version, got %d", version)
	}
}

func BenchmarkFanoutPublish(b *testing.B) {
	addr := getTestRedisAddr()
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()

	ctx := context.Background()
	// Kiểm tra kết nối trước
	if err := rdb.Ping(ctx).Err(); err != nil {
		b.Skipf("skipping benchmark: Redis not reachable at %s: %v", addr, err)
	}

	fanout := NewRedisFanout(rdb, "sync_channel")

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := fanout.Publish(ctx, "rbac_role:admin", []byte(`{"permissions":["read"]}`))
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
