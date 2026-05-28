package cache_test

import (
	"context"
	"testing"
	"time"

	iamCache "controlplane/internal/iam/cache"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func TestAdminAccessSessionCacheStoresDevicePublicKey(t *testing.T) {
	redisServer := miniredis.RunT(t)
	rds := goredis.NewClient(&goredis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = rds.Close() })

	cache := iamCache.NewAdminAccessSessionCache(rds)
	session := iamCache.AdminAccessSession{
		AccessKey:        "runtime-device-1",
		AccessSecretHash: "secret-hash",
		TrackedDeviceID:  "tracked-device-1",
		DevicePublicKey:  "public-key",
		TokenJTI:         "token-jti",
		Version:          1,
		LastSeenAt:       time.Now().UTC().Unix(),
	}
	if err := cache.SetAccessSession(context.Background(), session, time.Minute); err != nil {
		t.Fatalf("set runtime: %v", err)
	}

	stored, err := cache.GetAccessSession(context.Background(), session.AccessKey)
	if err != nil {
		t.Fatalf("get runtime: %v", err)
	}
	if stored == nil {
		t.Fatal("expected stored runtime")
	}
	if stored.DevicePublicKey != session.DevicePublicKey {
		t.Fatalf("device public key = %q, want %q", stored.DevicePublicKey, session.DevicePublicKey)
	}
}
