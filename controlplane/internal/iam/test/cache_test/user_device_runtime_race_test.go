package cache_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	iamCache "controlplane/internal/iam/cache"
	"controlplane/internal/security"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

// helper: spin up isolated cache.
func newRaceFixture(t *testing.T) (iamCache.UserDeviceRuntimeCache, func()) {
	t.Helper()
	srv := miniredis.RunT(t)
	rds := goredis.NewClient(&goredis.Options{Addr: srv.Addr()})
	cache := iamCache.NewUserDeviceRuntimeCache(rds)
	return cache, func() { _ = rds.Close() }
}

func seed(t *testing.T, cache iamCache.UserDeviceRuntimeCache, deviceID, jti, secret, userID, trackedRef string) {
	t.Helper()
	if err := cache.SetDeviceRuntime(context.Background(), iamCache.UserDeviceRuntime{
		AccessKey:        deviceID,
		AccessSecretHash: security.HashTokenSHA256(secret),
		CurrentJTI:       jti,
		TrackedDeviceID:  trackedRef,
		UserID:           userID,
		Status:           "online",
		Version:          1,
		LastSeenAt:       time.Now().UTC().Unix(),
		CurrentIssuedAt:  time.Now().UTC().Unix(),
	}, time.Minute); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
}

// Concurrent rotates with same expectedJTI: only one may succeed.
func TestRotateFragmentForJTIConcurrentSingleSuccess(t *testing.T) {
	cache, cleanup := newRaceFixture(t)
	defer cleanup()
	deviceID := "dev-init"
	userID := "user-r"
	seed(t, cache, deviceID, "jti-base", "secret-base", userID, "tracked-r")

	var wg sync.WaitGroup
	var success atomic.Int32
	const concurrency = 8
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ok, err := cache.RotateFragmentForUserDevice(
				context.Background(),
				userID,
				deviceID,
				"jti-base",
				"dev-new",
				security.HashTokenSHA256("secret-new"),
				"jti-new",
				time.Minute,
				nil, nil,
			)
			if err != nil {
				t.Errorf("rotate err: %v", err)
				return
			}
			if ok {
				success.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if success.Load() != 1 {
		t.Fatalf("expected exactly 1 successful rotate, got %d", success.Load())
	}
}

// Stale verify: after rotate, old jti should fail outside grace window.
func TestVerifyAfterRotateOldJTIFailsOutsideGrace(t *testing.T) {
	cache, cleanup := newRaceFixture(t)
	defer cleanup()
	deviceID := "dev-init"
	userID := "user-r2"
	seed(t, cache, deviceID, "jti-old", "secret-old", userID, "tracked-r2")

	if ok, err := cache.RotateFragmentForUserDevice(context.Background(), userID, deviceID, "jti-old", "dev-x", security.HashTokenSHA256("secret-new"), "jti-new", time.Minute, nil, nil); err != nil || !ok {
		t.Fatalf("seed rotate: ok=%v err=%v", ok, err)
	}
	record, err := cache.GetDeviceRuntimeByUserDevice(context.Background(), userID, "dev-x")
	if err != nil || record == nil {
		t.Fatalf("get runtime after rotate: %v", err)
	}
	// outside any grace -> reject
	if ok := iamCache.MatchRuntime(record, "dev-x", "secret-new", "jti-old", 0); ok {
		t.Fatal("expected stale jti to reject when grace=0")
	}
	// grace covers it
	if ok := iamCache.MatchRuntime(record, "dev-x", "secret-new", "jti-old", 30*time.Second); !ok {
		t.Fatal("expected stale jti to pass within grace window")
	}
}

// Delete is race-safe: rotate after delete returns ok=false (treated as stale by service).
func TestRotateAfterDeleteReturnsFalse(t *testing.T) {
	cache, cleanup := newRaceFixture(t)
	defer cleanup()
	deviceID := "dev-init"
	userID := "user-r3"
	seed(t, cache, deviceID, "jti-z", "secret-z", userID, "tracked-r3")
	if err := cache.DeleteDeviceRuntimeByUserDevice(context.Background(), userID, deviceID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	ok, err := cache.RotateFragmentForUserDevice(context.Background(), userID, deviceID, "jti-z", "dev-y", security.HashTokenSHA256("secret-y"), "jti-yy", time.Minute, nil, nil)
	if err != nil {
		t.Fatalf("rotate after delete err: %v", err)
	}
	if ok {
		t.Fatal("rotate must not succeed after delete")
	}
}

// Scan-by-prefix should not return deleted runtime key.
func TestScanByUserExcludesDeletedTracking(t *testing.T) {
	cache, cleanup := newRaceFixture(t)
	defer cleanup()
	uid := "user-scan"
	seed(t, cache, "dev-A", "jti-A", "secret-A", uid, "tracked-A")
	seed(t, cache, "dev-B", "jti-B", "secret-B", uid, "tracked-B")
	if err := cache.DeleteDeviceRuntimeByUserDevice(context.Background(), uid, "dev-A"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, err := cache.ScanByUser(context.Background(), uid, 50)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(got) != 1 || got[0].AccessKey != "dev-B" {
		t.Fatalf("expected only dev-B, got %#v", got)
	}
}
