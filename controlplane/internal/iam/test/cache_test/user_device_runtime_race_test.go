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

func seed(t *testing.T, cache iamCache.UserDeviceRuntimeCache, tracking, jti, secret, userID, trackedRef string) {
	t.Helper()
	if err := cache.SetDeviceRuntime(context.Background(), iamCache.UserDeviceRuntime{
		TrackingID:       tracking,
		DeviceID:         "dev-init",
		DeviceSecretHash: security.HashTokenSHA256(secret),
		CurrentJTI:       jti,
		TrackedDeviceRef: trackedRef,
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
	tracking := "race-1"
	seed(t, cache, tracking, "jti-base", "secret-base", "user-r", "tracked-r")

	var wg sync.WaitGroup
	var success atomic.Int32
	const concurrency = 8
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ok, err := cache.RotateFragmentForJTI(
				context.Background(),
				tracking,
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
	tracking := "race-2"
	seed(t, cache, tracking, "jti-old", "secret-old", "user-r2", "tracked-r2")

	if ok, err := cache.RotateFragmentForJTI(context.Background(), tracking, "jti-old", "dev-x", security.HashTokenSHA256("secret-new"), "jti-new", time.Minute, nil, nil); err != nil || !ok {
		t.Fatalf("seed rotate: ok=%v err=%v", ok, err)
	}
	// outside any grace -> reject
	if ok, _ := cache.VerifyFragmentAndJTI(context.Background(), tracking, "dev-x", "secret-new", "jti-old", 0); ok {
		t.Fatal("expected stale jti to reject when grace=0")
	}
	// grace covers it
	if ok, _ := cache.VerifyFragmentAndJTI(context.Background(), tracking, "dev-x", "secret-new", "jti-old", 30*time.Second); !ok {
		t.Fatal("expected stale jti to pass within grace window")
	}
}

// Delete is race-safe: rotate after delete returns ok=false (treated as stale by service).
func TestRotateAfterDeleteReturnsFalse(t *testing.T) {
	cache, cleanup := newRaceFixture(t)
	defer cleanup()
	tracking := "race-3"
	seed(t, cache, tracking, "jti-z", "secret-z", "user-r3", "tracked-r3")
	if err := cache.DeleteDeviceRuntime(context.Background(), tracking); err != nil {
		t.Fatalf("delete: %v", err)
	}
	ok, err := cache.RotateFragmentForJTI(context.Background(), tracking, "jti-z", "dev-y", security.HashTokenSHA256("secret-y"), "jti-yy", time.Minute, nil, nil)
	if err != nil {
		t.Fatalf("rotate after delete err: %v", err)
	}
	if ok {
		t.Fatal("rotate must not succeed after delete")
	}
}

// Index entry is consistent after delete: ScanByUser should not return ghost.
func TestScanByUserExcludesDeletedTracking(t *testing.T) {
	cache, cleanup := newRaceFixture(t)
	defer cleanup()
	uid := "user-scan"
	seed(t, cache, "track-A", "jti-A", "secret-A", uid, "tracked-A")
	seed(t, cache, "track-B", "jti-B", "secret-B", uid, "tracked-B")
	if err := cache.DeleteDeviceRuntime(context.Background(), "track-A"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, err := cache.ScanByUser(context.Background(), uid, 50)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(got) != 1 || got[0].TrackingID != "track-B" {
		t.Fatalf("expected only track-B, got %#v", got)
	}
}
