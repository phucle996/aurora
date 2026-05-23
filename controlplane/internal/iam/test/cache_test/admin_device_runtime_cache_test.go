package cache_test

import (
	"context"
	"testing"
	"time"

	iamCache "controlplane/internal/iam/cache"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func TestAdminDeviceRuntimeCacheStoresDevicePublicKey(t *testing.T) {
	redisServer := miniredis.RunT(t)
	rds := goredis.NewClient(&goredis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = rds.Close() })

	cache := iamCache.NewAdminDeviceRuntimeCache(rds)
	runtime := iamCache.AdminDeviceRuntime{
		DeviceID:         "runtime-device-1",
		DeviceSecretHash: "secret-hash",
		TrackedDeviceID:  "tracked-device-1",
		DevicePublicKey:  "public-key",
		TokenJTI:         "token-jti",
		Version:          1,
		LastSeenAt:       time.Now().UTC().Unix(),
	}
	if err := cache.SetDeviceRuntime(context.Background(), runtime, time.Minute); err != nil {
		t.Fatalf("set runtime: %v", err)
	}

	stored, err := cache.GetDeviceRuntime(context.Background(), runtime.DeviceID)
	if err != nil {
		t.Fatalf("get runtime: %v", err)
	}
	if stored == nil {
		t.Fatal("expected stored runtime")
	}
	if stored.DevicePublicKey != runtime.DevicePublicKey {
		t.Fatalf("device public key = %q, want %q", stored.DevicePublicKey, runtime.DevicePublicKey)
	}
}
