package svc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"controlplane/infra/telegram"
	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcImpl "controlplane/internal/iam/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"controlplane/pkg/apperr"
	"controlplane/pkg/constant"

	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

type l2CacheMock struct {
	getFn    func(ctx context.Context, key string) (payload []byte, version int64, exists bool, err error)
	setFn    func(ctx context.Context, key string, data interface{}, version int64, ttl time.Duration) error
	deleteFn func(ctx context.Context, key string) error
}

func (m *l2CacheMock) Get(ctx context.Context, key string) (payload []byte, version int64, exists bool, err error) {
	if m.getFn != nil {
		return m.getFn(ctx, key)
	}
	return nil, 0, false, nil
}

func (m *l2CacheMock) Set(ctx context.Context, key string, data interface{}, version int64, ttl time.Duration) error {
	if m.setFn != nil {
		return m.setFn(ctx, key, data, version, ttl)
	}
	return nil
}

func (m *l2CacheMock) Delete(ctx context.Context, key string) error {
	if m.deleteFn != nil {
		return m.deleteFn(ctx, key)
	}
	return nil
}

func (m *l2CacheMock) Client() *redis.Client {
	return nil
}

func (m *l2CacheMock) GetOrLoad(ctx context.Context, key string, target interface{}, ttl time.Duration, loadFn func() (interface{}, error)) (version int64, err error) {
	return 0, nil
}

type execMock struct {
	executeFn func(ctx context.Context, script string, keys []string, args ...interface{}) (interface{}, error)
}

func (m *execMock) Execute(ctx context.Context, script string, keys []string, args ...interface{}) (interface{}, error) {
	if m.executeFn != nil {
		return m.executeFn(ctx, script, keys, args...)
	}
	return nil, nil
}

func TestAdminLoginInvalidArgumentReturnsAppError(t *testing.T) {
	exec := &execMock{executeFn: func(ctx context.Context, script string, keys []string, args ...interface{}) (interface{}, error) {
		return int64(1), nil
	}}
	registry := &cacheengine.CacheRegistry{
		Exec: exec,
	}
	svc := iamSvcImpl.NewAdminAPIKeyService(config.LoadConfig(), &adminBootstrapRepoMock{}, telegram.NewTelegramClient("", ""), registry)

	_, err := svc.AdminLogin(context.Background(), iamEntity.AdminLoginRequest{})
	if !errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
		t.Fatalf("expected invalid argument kind, got %v", err)
	}
	appErr, ok := apperr.As(err)
	if !ok || appErr == nil {
		t.Fatalf("expected app error envelope")
	}
	if appErr.Outcome != "failure" {
		t.Fatalf("unexpected outcome: %q", appErr.Outcome)
	}
}

func TestRefreshInvalidArgumentReturnsAppError(t *testing.T) {
	registry := &cacheengine.CacheRegistry{}
	svc := iamSvcImpl.NewSessionRefreshService(config.LoadConfig(), nil, nil, registry)

	ident := &constant.Identity{AccessKey: " "}
	ctx := context.WithValue(context.Background(), constant.IdentityKey, ident)
	_, err := svc.RefreshAdminTrinity(ctx, "global", nil, nil)
	if !errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
		t.Fatalf("expected invalid argument kind, got %v", err)
	}
	appErr, ok := apperr.As(err)
	if !ok || appErr == nil {
		t.Fatalf("expected app error envelope")
	}
	if appErr.Outcome != "failure" {
		t.Fatalf("unexpected outcome: %q", appErr.Outcome)
	}
}

func TestAdminLogoutLoadRuntimeErrorWrapsCause(t *testing.T) {
	raw := errors.New("redis down")
	l2Mock := &l2CacheMock{getFn: func(ctx context.Context, key string) ([]byte, int64, bool, error) {
		return nil, 0, false, raw
	}}
	registry := &cacheengine.CacheRegistry{L2: l2Mock}
	svc := iamSvcImpl.NewAdminAPIKeyService(config.LoadConfig(), &adminBootstrapRepoMock{}, telegram.NewTelegramClient("", ""), registry)

	ctx := context.WithValue(context.Background(), constant.IdentityKey, &constant.Identity{AccessKey: "device-1"})
	err := svc.AdminLogout(ctx, nil, nil)
	if !errors.Is(err, iamTaxonomy.ErrInternalError) {
		t.Fatalf("expected ErrInternalError, got %v", err)
	}
	appErr, ok := apperr.As(err)
	if !ok || appErr == nil {
		t.Fatalf("expected app error envelope")
	}
	if appErr.Outcome != "failure_unknown" {
		t.Fatalf("unexpected outcome: %q", appErr.Outcome)
	}
	if !errors.Is(appErr.Cause, raw) {
		t.Fatalf("expected raw cause preserved")
	}
}

func TestRefreshLoadRuntimeErrorReturnsInternalKind(t *testing.T) {
	raw := errors.New("redis timeout")
	l2Mock := &l2CacheMock{getFn: func(ctx context.Context, key string) ([]byte, int64, bool, error) {
		return nil, 0, false, raw
	}}
	registry := &cacheengine.CacheRegistry{L2: l2Mock}
	svc := iamSvcImpl.NewSessionRefreshService(config.LoadConfig(), nil, nil, registry)

	ident := &constant.Identity{AccessKey: "device-1"}
	ctx := context.WithValue(context.Background(), constant.IdentityKey, ident)
	_, err := svc.RefreshAdminTrinity(ctx, "global", nil, nil)
	if !errors.Is(err, iamTaxonomy.ErrInternalError) {
		t.Fatalf("expected ErrInternalError, got %v", err)
	}
	appErr, ok := apperr.As(err)
	if !ok || appErr == nil {
		t.Fatalf("expected app error envelope")
	}
	if appErr.Outcome != "failure_unknown" {
		t.Fatalf("unexpected outcome: %q", appErr.Outcome)
	}
	if !errors.Is(appErr.Cause, raw) {
		t.Fatalf("expected raw cause preserved")
	}
}

func TestAdminLogoutSkipsDBFlushWhenLastSeenNotDirty(t *testing.T) {
	touchCalls := 0
	repo := &adminBootstrapRepoMock{touchLastSeenFn: func(ctx context.Context, deviceID string, ip *string, userAgent *string, seenAt time.Time) error {
		touchCalls++
		return nil
	}}
	deleteCalls := 0
	l2Mock := &l2CacheMock{
		getFn: func(ctx context.Context, key string) ([]byte, int64, bool, error) {
			pbAdmin := &iamproto.AdminAccessSession{
				AccessKey:         "device-1",
				TrackedDeviceId:   "tracked-1",
				LastSeenAt:        time.Now().UTC().Unix(),
				LastSeenDirty:     false,
			}
			payload, _ := proto.Marshal(pbAdmin)
			return payload, 1, true, nil
		},
		deleteFn: func(ctx context.Context, key string) error {
			deleteCalls++
			return nil
		},
	}
	registry := &cacheengine.CacheRegistry{L2: l2Mock}
	svc := iamSvcImpl.NewAdminAPIKeyService(config.LoadConfig(), repo, telegram.NewTelegramClient("", ""), registry)

	ctx := context.WithValue(context.Background(), constant.IdentityKey, &constant.Identity{AccessKey: "device-1"})
	if err := svc.AdminLogout(ctx, nil, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if touchCalls != 0 {
		t.Fatalf("expected no db flush when last_seen not dirty")
	}
	if deleteCalls != 1 {
		t.Fatalf("expected runtime delete once, got %d", deleteCalls)
	}
}

func TestAdminLogoutFlushesDBWhenLastSeenDirty(t *testing.T) {
	touchCalls := 0
	repo := &adminBootstrapRepoMock{touchLastSeenFn: func(ctx context.Context, deviceID string, ip *string, userAgent *string, seenAt time.Time) error {
		touchCalls++
		if ip == nil || *ip != "10.0.0.1" {
			t.Fatalf("expected ip from runtime")
		}
		if userAgent == nil || *userAgent != "ua-1" {
			t.Fatalf("expected userAgent from runtime")
		}
		return nil
	}}
	l2Mock := &l2CacheMock{
		getFn: func(ctx context.Context, key string) ([]byte, int64, bool, error) {
			pbAdmin := &iamproto.AdminAccessSession{
				AccessKey:         "device-1",
				TrackedDeviceId:   "tracked-1",
				LastSeenAt:        time.Now().UTC().Unix(),
				LastSeenIp:        "10.0.0.1",
				LastSeenUserAgent: "ua-1",
				LastSeenDirty:     true,
			}
			payload, _ := proto.Marshal(pbAdmin)
			return payload, 1, true, nil
		},
	}
	registry := &cacheengine.CacheRegistry{L2: l2Mock}
	svc := iamSvcImpl.NewAdminAPIKeyService(config.LoadConfig(), repo, telegram.NewTelegramClient("", ""), registry)

	ctx := context.WithValue(context.Background(), constant.IdentityKey, &constant.Identity{AccessKey: "device-1"})
	if err := svc.AdminLogout(ctx, nil, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Đợi Goroutine chạy nền cập nhật DB hoàn tất
	time.Sleep(10 * time.Millisecond)
	if touchCalls != 1 {
		t.Fatalf("expected db flush once when dirty, got %d", touchCalls)
	}
}
