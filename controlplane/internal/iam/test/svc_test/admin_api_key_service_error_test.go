package svc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"controlplane/infra/telegram"
	"controlplane/internal/config"
	iamCache "controlplane/internal/iam/cache"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamErrorx "controlplane/internal/iam/errorx"
	iamSvcImpl "controlplane/internal/iam/service"
	"controlplane/pkg/apperr"
)

type adminDeviceRuntimeCacheMock struct {
	getFn     func(ctx context.Context, deviceID string) (*iamCache.AdminDeviceRuntime, error)
	compareFn func(ctx context.Context, deviceID string, expectedVersion int64, ttl time.Duration, ip *string, userAgent *string) (bool, error)
	scanFn    func(ctx context.Context, limit int) ([]iamCache.AdminDeviceRuntime, error)
	deleteFn  func(ctx context.Context, deviceID string) error
}

func (m *adminDeviceRuntimeCacheMock) SetDeviceRuntime(ctx context.Context, runtime iamCache.AdminDeviceRuntime, ttl time.Duration) error {
	return nil
}
func (m *adminDeviceRuntimeCacheMock) VerifyDeviceSecret(ctx context.Context, deviceID string, rawDeviceSecret string) (bool, error) {
	return false, nil
}
func (m *adminDeviceRuntimeCacheMock) GetDeviceRuntime(ctx context.Context, deviceID string) (*iamCache.AdminDeviceRuntime, error) {
	if m.getFn != nil {
		return m.getFn(ctx, deviceID)
	}
	return nil, nil
}
func (m *adminDeviceRuntimeCacheMock) TouchDeviceSecret(ctx context.Context, deviceID string, ttl time.Duration) error {
	return nil
}
func (m *adminDeviceRuntimeCacheMock) CompareAndTouchDeviceRuntime(ctx context.Context, deviceID string, expectedVersion int64, ttl time.Duration, ip *string, userAgent *string) (bool, error) {
	if m.compareFn != nil {
		return m.compareFn(ctx, deviceID, expectedVersion, ttl, ip, userAgent)
	}
	return false, nil
}
func (m *adminDeviceRuntimeCacheMock) ScanDeviceRuntimes(ctx context.Context, limit int) ([]iamCache.AdminDeviceRuntime, error) {
	if m.scanFn != nil {
		return m.scanFn(ctx, limit)
	}
	return nil, nil
}
func (m *adminDeviceRuntimeCacheMock) DeleteDeviceSecret(ctx context.Context, deviceID string) error {
	if m.deleteFn != nil {
		return m.deleteFn(ctx, deviceID)
	}
	return nil
}

func TestAdminLoginInvalidArgumentReturnsAppError(t *testing.T) {
	svc := iamSvcImpl.NewAdminAPIKeyService(config.LoadConfig(), &adminBootstrapRepoMock{}, telegram.NewTelegramClient("", ""), nil, nil, nil)

	_, err := svc.AdminLogin(context.Background(), iamEntity.AdminLoginRequest{})
	if !errors.Is(err, iamErrorx.ErrInvalidArgument) {
		t.Fatalf("expected invalid argument kind, got %v", err)
	}
	appErr, ok := apperr.As(err)
	if !ok || appErr == nil {
		t.Fatalf("expected app error envelope")
	}
	if appErr.Reason != iamErrorx.ReasonAdminLoginInvalidArgument {
		t.Fatalf("unexpected reason: %q", appErr.Reason)
	}
}

func TestRefreshInvalidArgumentReturnsAppError(t *testing.T) {
	svc := iamSvcImpl.NewAdminAPIKeyService(config.LoadConfig(), &adminBootstrapRepoMock{}, telegram.NewTelegramClient("", ""), nil, nil, nil)

	_, err := svc.RefreshAdminSession(context.Background(), " ", nil, nil)
	if !errors.Is(err, iamErrorx.ErrInvalidArgument) {
		t.Fatalf("expected invalid argument kind, got %v", err)
	}
	appErr, ok := apperr.As(err)
	if !ok || appErr == nil {
		t.Fatalf("expected app error envelope")
	}
	if appErr.Reason != iamErrorx.ReasonAdminRefreshInvalidArgument {
		t.Fatalf("unexpected reason: %q", appErr.Reason)
	}
}

func TestAdminLogoutLoadRuntimeErrorWrapsCause(t *testing.T) {
	raw := errors.New("redis down")
	deviceRT := &adminDeviceRuntimeCacheMock{getFn: func(ctx context.Context, deviceID string) (*iamCache.AdminDeviceRuntime, error) {
		return nil, raw
	}}
	svc := iamSvcImpl.NewAdminAPIKeyService(config.LoadConfig(), &adminBootstrapRepoMock{}, telegram.NewTelegramClient("", ""), nil, deviceRT, nil)

	err := svc.AdminLogout(context.Background(), "device-1", nil, nil)
	if !errors.Is(err, iamErrorx.ErrInvalidArgument) {
		t.Fatalf("expected invalid argument kind, got %v", err)
	}
	appErr, ok := apperr.As(err)
	if !ok || appErr == nil {
		t.Fatalf("expected app error envelope")
	}
	if appErr.Reason != iamErrorx.ReasonAdminLogoutCacheError {
		t.Fatalf("unexpected reason: %q", appErr.Reason)
	}
	if !errors.Is(appErr.Cause, raw) {
		t.Fatalf("expected raw cause preserved")
	}
}

func TestRefreshLoadRuntimeErrorReturnsInternalKind(t *testing.T) {
	raw := errors.New("redis timeout")
	deviceRT := &adminDeviceRuntimeCacheMock{getFn: func(ctx context.Context, deviceID string) (*iamCache.AdminDeviceRuntime, error) {
		return nil, raw
	}}
	svc := iamSvcImpl.NewAdminAPIKeyService(config.LoadConfig(), &adminBootstrapRepoMock{}, telegram.NewTelegramClient("", ""), nil, deviceRT, nil)

	_, err := svc.RefreshAdminSession(context.Background(), "device-1", nil, nil)
	if !errors.Is(err, iamErrorx.ErrAuthenticationUnavailable) {
		t.Fatalf("expected authentication unavailable kind, got %v", err)
	}
	appErr, ok := apperr.As(err)
	if !ok || appErr == nil {
		t.Fatalf("expected app error envelope")
	}
	if appErr.Reason != iamErrorx.ReasonAdminRefreshCacheError {
		t.Fatalf("unexpected reason: %q", appErr.Reason)
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
	deviceRT := &adminDeviceRuntimeCacheMock{
		getFn: func(ctx context.Context, deviceID string) (*iamCache.AdminDeviceRuntime, error) {
			return &iamCache.AdminDeviceRuntime{DeviceID: deviceID, TrackedDeviceID: "tracked-1", LastSeenAt: time.Now().UTC().Unix(), LastSeenDirty: false}, nil
		},
		deleteFn: func(ctx context.Context, deviceID string) error {
			deleteCalls++
			return nil
		},
	}
	svc := iamSvcImpl.NewAdminAPIKeyService(config.LoadConfig(), repo, telegram.NewTelegramClient("", ""), nil, deviceRT, nil)

	if err := svc.AdminLogout(context.Background(), "device-1", nil, nil); err != nil {
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
	deviceRT := &adminDeviceRuntimeCacheMock{
		getFn: func(ctx context.Context, deviceID string) (*iamCache.AdminDeviceRuntime, error) {
			return &iamCache.AdminDeviceRuntime{DeviceID: deviceID, TrackedDeviceID: "tracked-1", LastSeenAt: time.Now().UTC().Unix(), LastSeenIP: "10.0.0.1", LastSeenUserAgent: "ua-1", LastSeenDirty: true}, nil
		},
	}
	svc := iamSvcImpl.NewAdminAPIKeyService(config.LoadConfig(), repo, telegram.NewTelegramClient("", ""), nil, deviceRT, nil)

	if err := svc.AdminLogout(context.Background(), "device-1", nil, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if touchCalls != 1 {
		t.Fatalf("expected db flush once when dirty, got %d", touchCalls)
	}
}
