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
	iamSvcImpl "controlplane/internal/iam/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/pkg/apperr"
	"controlplane/pkg/constant"
)

type adminAccessSessionCacheMock struct {
	getFn     func(ctx context.Context, accessKey string) (*iamCache.AdminAccessSession, error)
	compareFn func(ctx context.Context, accessKey string, expectedVersion int64, ttl time.Duration, ip *string, userAgent *string) (bool, error)
	scanFn    func(ctx context.Context, limit int) ([]iamCache.AdminAccessSession, error)
	deleteFn  func(ctx context.Context, accessKey string) error
}

func (m *adminAccessSessionCacheMock) SetAccessSession(ctx context.Context, session iamCache.AdminAccessSession, ttl time.Duration) error {
	return nil
}
func (m *adminAccessSessionCacheMock) VerifyAccessSecret(ctx context.Context, accessKey string, rawAccessSecret string) (bool, error) {
	return false, nil
}
func (m *adminAccessSessionCacheMock) GetAccessSession(ctx context.Context, accessKey string) (*iamCache.AdminAccessSession, error) {
	if m.getFn != nil {
		return m.getFn(ctx, accessKey)
	}
	return nil, nil
}
func (m *adminAccessSessionCacheMock) TouchAccessSession(ctx context.Context, accessKey string, ttl time.Duration) error {
	return nil
}
func (m *adminAccessSessionCacheMock) CompareAndTouchAccessSession(ctx context.Context, accessKey string, expectedVersion int64, ttl time.Duration, ip *string, userAgent *string) (bool, error) {
	if m.compareFn != nil {
		return m.compareFn(ctx, accessKey, expectedVersion, ttl, ip, userAgent)
	}
	return false, nil
}
func (m *adminAccessSessionCacheMock) ScanAccessSessions(ctx context.Context, limit int) ([]iamCache.AdminAccessSession, error) {
	if m.scanFn != nil {
		return m.scanFn(ctx, limit)
	}
	return nil, nil
}
func (m *adminAccessSessionCacheMock) DeleteAccessSession(ctx context.Context, accessKey string) error {
	if m.deleteFn != nil {
		return m.deleteFn(ctx, accessKey)
	}
	return nil
}

func TestAdminLoginInvalidArgumentReturnsAppError(t *testing.T) {
	svc := iamSvcImpl.NewAdminAPIKeyService(config.LoadConfig(), &adminBootstrapRepoMock{}, telegram.NewTelegramClient("", ""), nil, nil, nil)

	_, err := svc.AdminLogin(context.Background(), iamEntity.AdminLoginRequest{})
	if !errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
		t.Fatalf("expected invalid argument kind, got %v", err)
	}
	appErr, ok := apperr.As(err)
	if !ok || appErr == nil {
		t.Fatalf("expected app error envelope")
	}
	if appErr.Outcome != iamTaxonomy.AdminLoginOutcomeInvalidArgument {
		t.Fatalf("unexpected outcome: %q", appErr.Outcome)
	}
}

func TestRefreshInvalidArgumentReturnsAppError(t *testing.T) {
	svc := iamSvcImpl.NewAdminAPIKeyService(config.LoadConfig(), &adminBootstrapRepoMock{}, telegram.NewTelegramClient("", ""), nil, nil, nil)

	ctx := context.WithValue(context.Background(), constant.ContextKeyAdminAccessKey, " ")
	_, err := svc.RefreshAdminSession(ctx, "global", nil, nil)
	if !errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
		t.Fatalf("expected invalid argument kind, got %v", err)
	}
	appErr, ok := apperr.As(err)
	if !ok || appErr == nil {
		t.Fatalf("expected app error envelope")
	}
	if appErr.Outcome != iamTaxonomy.AdminRefreshOutcomeInvalidArgument {
		t.Fatalf("unexpected outcome: %q", appErr.Outcome)
	}
}

func TestAdminLogoutLoadRuntimeErrorWrapsCause(t *testing.T) {
	raw := errors.New("redis down")
	sessionCache := &adminAccessSessionCacheMock{getFn: func(ctx context.Context, accessKey string) (*iamCache.AdminAccessSession, error) {
		return nil, raw
	}}
	svc := iamSvcImpl.NewAdminAPIKeyService(config.LoadConfig(), &adminBootstrapRepoMock{}, telegram.NewTelegramClient("", ""), sessionCache, nil, nil)

	err := svc.AdminLogout(context.Background(), "device-1", nil, nil)
	if !errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
		t.Fatalf("expected invalid argument kind, got %v", err)
	}
	appErr, ok := apperr.As(err)
	if !ok || appErr == nil {
		t.Fatalf("expected app error envelope")
	}
	if appErr.Outcome != iamTaxonomy.AdminLogoutOutcomeSystemError {
		t.Fatalf("unexpected outcome: %q", appErr.Outcome)
	}
	if !errors.Is(appErr.Cause, raw) {
		t.Fatalf("expected raw cause preserved")
	}
}

func TestRefreshLoadRuntimeErrorReturnsInternalKind(t *testing.T) {
	raw := errors.New("redis timeout")
	sessionCache := &adminAccessSessionCacheMock{getFn: func(ctx context.Context, accessKey string) (*iamCache.AdminAccessSession, error) {
		return nil, raw
	}}
	svc := iamSvcImpl.NewAdminAPIKeyService(config.LoadConfig(), &adminBootstrapRepoMock{}, telegram.NewTelegramClient("", ""), sessionCache, nil, nil)

	ctx := context.WithValue(context.Background(), constant.ContextKeyAdminAccessKey, "device-1")
	_, err := svc.RefreshAdminSession(ctx, "global", nil, nil)
	if !errors.Is(err, iamTaxonomy.ErrAuthenticationUnavailable) {
		t.Fatalf("expected authentication unavailable kind, got %v", err)
	}
	appErr, ok := apperr.As(err)
	if !ok || appErr == nil {
		t.Fatalf("expected app error envelope")
	}
	if appErr.Outcome != iamTaxonomy.AdminRefreshOutcomeSystemError {
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
	sessionCache := &adminAccessSessionCacheMock{
		getFn: func(ctx context.Context, accessKey string) (*iamCache.AdminAccessSession, error) {
			return &iamCache.AdminAccessSession{AccessKey: accessKey, TrackedDeviceID: "tracked-1", LastSeenAt: time.Now().UTC().Unix(), LastSeenDirty: false}, nil
		},
		deleteFn: func(ctx context.Context, accessKey string) error {
			deleteCalls++
			return nil
		},
	}
	svc := iamSvcImpl.NewAdminAPIKeyService(config.LoadConfig(), repo, telegram.NewTelegramClient("", ""), sessionCache, nil, nil)

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
	sessionCache := &adminAccessSessionCacheMock{
		getFn: func(ctx context.Context, accessKey string) (*iamCache.AdminAccessSession, error) {
			return &iamCache.AdminAccessSession{AccessKey: accessKey, TrackedDeviceID: "tracked-1", LastSeenAt: time.Now().UTC().Unix(), LastSeenIP: "10.0.0.1", LastSeenUserAgent: "ua-1", LastSeenDirty: true}, nil
		},
	}
	svc := iamSvcImpl.NewAdminAPIKeyService(config.LoadConfig(), repo, telegram.NewTelegramClient("", ""), sessionCache, nil, nil)

	if err := svc.AdminLogout(context.Background(), "device-1", nil, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Đợi Goroutine chạy nền cập nhật DB hoàn tất
	time.Sleep(10 * time.Millisecond)
	if touchCalls != 1 {
		t.Fatalf("expected db flush once when dirty, got %d", touchCalls)
	}
}
