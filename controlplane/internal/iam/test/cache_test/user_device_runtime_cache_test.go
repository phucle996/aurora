package cache_test

import (
	"context"
	"testing"
	"time"

	iamCache "controlplane/internal/iam/cache"
	"controlplane/internal/security"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func newUserRuntimeFixture(t *testing.T) (iamCache.UserDeviceRuntimeCache, func()) {
	t.Helper()
	redisServer := miniredis.RunT(t)
	rds := goredis.NewClient(&goredis.Options{Addr: redisServer.Addr()})
	cache := iamCache.NewUserDeviceRuntimeCache(rds)
	cleanup := func() { _ = rds.Close() }
	return cache, cleanup
}

func TestUserDeviceRuntimeSetGet(t *testing.T) {
	cache, cleanup := newUserRuntimeFixture(t)
	defer cleanup()
	ctx := context.Background()

	runtime := iamCache.UserDeviceRuntime{
		TrackingID:       "track-1",
		DeviceID:         "dev-1",
		DeviceSecretHash: security.HashTokenSHA256("secret-1"),
		CurrentJTI:       "jti-1",
		TrackedDeviceRef: "tracked-1",
		UserID:           "user-1",
		Status:           "online",
	}
	if err := cache.SetDeviceRuntime(ctx, runtime, time.Minute); err != nil {
		t.Fatalf("set runtime: %v", err)
	}

	stored, err := cache.GetDeviceRuntime(ctx, runtime.TrackingID)
	if err != nil || stored == nil {
		t.Fatalf("get runtime: %v %v", stored, err)
	}
	if stored.DeviceID != runtime.DeviceID || stored.CurrentJTI != runtime.CurrentJTI {
		t.Fatalf("unexpected stored runtime: %#v", stored)
	}
}

func TestUserDeviceRuntimeVerifyMismatch(t *testing.T) {
	cache, cleanup := newUserRuntimeFixture(t)
	defer cleanup()
	ctx := context.Background()

	runtime := iamCache.UserDeviceRuntime{
		TrackingID:       "track-2",
		DeviceID:         "dev-2",
		DeviceSecretHash: security.HashTokenSHA256("secret-2"),
		CurrentJTI:       "jti-2",
		TrackedDeviceRef: "tracked-2",
		UserID:           "user-2",
	}
	if err := cache.SetDeviceRuntime(ctx, runtime, time.Minute); err != nil {
		t.Fatalf("set runtime: %v", err)
	}

	cases := []struct {
		name        string
		deviceID    string
		secret      string
		jti         string
		expectMatch bool
	}{
		{"happy", "dev-2", "secret-2", "jti-2", true},
		{"wrong-device", "dev-x", "secret-2", "jti-2", false},
		{"wrong-secret", "dev-2", "secret-x", "jti-2", false},
		{"wrong-jti", "dev-2", "secret-2", "jti-x", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := cache.VerifyFragmentAndJTI(ctx, runtime.TrackingID, tc.deviceID, tc.secret, tc.jti, 0)
			if err != nil {
				t.Fatalf("verify err: %v", err)
			}
			if ok != tc.expectMatch {
				t.Fatalf("verify ok=%v want=%v", ok, tc.expectMatch)
			}
		})
	}
}

func TestUserDeviceRuntimeRotateAndGrace(t *testing.T) {
	cache, cleanup := newUserRuntimeFixture(t)
	defer cleanup()
	ctx := context.Background()

	runtime := iamCache.UserDeviceRuntime{
		TrackingID:       "track-3",
		DeviceID:         "dev-old",
		DeviceSecretHash: security.HashTokenSHA256("secret-old"),
		CurrentJTI:       "jti-old",
		TrackedDeviceRef: "tracked-3",
		UserID:           "user-3",
	}
	if err := cache.SetDeviceRuntime(ctx, runtime, time.Minute); err != nil {
		t.Fatalf("set runtime: %v", err)
	}

	ok, err := cache.RotateFragmentForJTI(ctx, runtime.TrackingID, "jti-old", "dev-new", security.HashTokenSHA256("secret-new"), "jti-new", time.Minute, nil, nil)
	if err != nil || !ok {
		t.Fatalf("rotate failed: ok=%v err=%v", ok, err)
	}

	// new jti must verify; old jti must verify ONLY within grace window
	if ok, _ := cache.VerifyFragmentAndJTI(ctx, runtime.TrackingID, "dev-new", "secret-new", "jti-new", 0); !ok {
		t.Fatal("new jti should verify")
	}
	if ok, _ := cache.VerifyFragmentAndJTI(ctx, runtime.TrackingID, "dev-new", "secret-new", "jti-old", 0); ok {
		t.Fatal("old jti must not verify without grace")
	}
	if ok, _ := cache.VerifyFragmentAndJTI(ctx, runtime.TrackingID, "dev-new", "secret-new", "jti-old", 5*time.Second); !ok {
		t.Fatal("old jti must verify within grace window")
	}
}

func TestUserDeviceRuntimeDelete(t *testing.T) {
	cache, cleanup := newUserRuntimeFixture(t)
	defer cleanup()
	ctx := context.Background()

	runtime := iamCache.UserDeviceRuntime{
		TrackingID:       "track-4",
		DeviceID:         "dev-4",
		DeviceSecretHash: security.HashTokenSHA256("secret-4"),
		CurrentJTI:       "jti-4",
		TrackedDeviceRef: "tracked-4",
		UserID:           "user-4",
	}
	if err := cache.SetDeviceRuntime(ctx, runtime, time.Minute); err != nil {
		t.Fatalf("set runtime: %v", err)
	}
	if err := cache.DeleteDeviceRuntime(ctx, runtime.TrackingID); err != nil {
		t.Fatalf("delete runtime: %v", err)
	}
	stored, _ := cache.GetDeviceRuntime(ctx, runtime.TrackingID)
	if stored != nil {
		t.Fatalf("expected nil after delete, got %#v", stored)
	}
}
