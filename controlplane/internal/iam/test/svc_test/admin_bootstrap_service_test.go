package svc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"controlplane/infra/telegram"
	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcImpl "controlplane/internal/iam/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/internal/security"
)

type bootstrapLockMock struct{}

func (bootstrapLockMock) Release(ctx context.Context) error { return nil }

type adminBootstrapRepoMock struct {
	acquireFn         func(ctx context.Context) (iamRepoInterface.BootstrapLock, error)
	activeFn          func(ctx context.Context) (*iamEntity.AdminAPIKey, error)
	bootstrapFn       func(ctx context.Context, payload iamEntity.AdminBootstrapPayload) (time.Time, error)
	rollbackFn        func(ctx context.Context, payload iamEntity.AdminBootstrapPayload) error
	get2FAFn          func(ctx context.Context) (*iamEntity.Admin2FASettings, error)
	consumeRecoveryFn func(ctx context.Context, codeHash string, now time.Time) (bool, error)
	upsertDeviceFn    func(ctx context.Context, input iamEntity.AdminDeviceBindingInput) (*iamEntity.AdminDevice, error)
	getDeviceByIDFn   func(ctx context.Context, deviceID string) (*iamEntity.AdminDevice, error)
	touchLastSeenFn   func(ctx context.Context, deviceID string, ip *string, userAgent *string, seenAt time.Time) error
}

func (m *adminBootstrapRepoMock) AcquireBootstrapLock(ctx context.Context) (iamRepoInterface.BootstrapLock, error) {
	return m.acquireFn(ctx)
}
func (m *adminBootstrapRepoMock) GetActiveAdminAPIKey(ctx context.Context) (*iamEntity.AdminAPIKey, error) {
	return m.activeFn(ctx)
}
func (m *adminBootstrapRepoMock) Bootstrap(ctx context.Context, payload iamEntity.AdminBootstrapPayload) (time.Time, error) {
	return m.bootstrapFn(ctx, payload)
}
func (m *adminBootstrapRepoMock) RollbackBootstrap(ctx context.Context, payload iamEntity.AdminBootstrapPayload) error {
	return m.rollbackFn(ctx, payload)
}
func (m *adminBootstrapRepoMock) AcquireRotationLock(ctx context.Context) (iamRepoInterface.BootstrapLock, error) {
	return nil, nil
}
func (m *adminBootstrapRepoMock) PrepareNextAdminAPIKey(ctx context.Context, key iamEntity.AdminAPIKey) error {
	return nil
}

func TestAdminBootstrapLockFailed(t *testing.T) {
	repo := &adminBootstrapRepoMock{
		acquireFn: func(ctx context.Context) (iamRepoInterface.BootstrapLock, error) { return nil, errors.New("lock") },
	}
	svc := iamSvcImpl.NewAdminAPIKeyService(config.LoadConfig(), repo, telegram.NewTelegramClient("", ""), nil, nil, nil, nil)
	err := svc.Bootstrap(context.Background(), "tester")
	if !errors.Is(err, iamTaxonomy.ErrAdminBootstrapLockFailed) {
		t.Fatalf("expected ErrAdminBootstrapLockFailed, got %v", err)
	}
}

func TestAdminBootstrapNotAllowedWhenActiveKeyExists(t *testing.T) {
	repo := &adminBootstrapRepoMock{
		acquireFn: func(ctx context.Context) (iamRepoInterface.BootstrapLock, error) { return bootstrapLockMock{}, nil },
		activeFn: func(ctx context.Context) (*iamEntity.AdminAPIKey, error) {
			return &iamEntity.AdminAPIKey{KeyHash: "x", ExpiresAt: time.Now().UTC().Add(time.Hour)}, nil
		},
	}
	svc := iamSvcImpl.NewAdminAPIKeyService(config.LoadConfig(), repo, telegram.NewTelegramClient("", ""), nil, nil, nil, nil)
	err := svc.Bootstrap(context.Background(), "tester")
	if !errors.Is(err, iamTaxonomy.ErrAdminBootstrapNotAllowed) {
		t.Fatalf("expected ErrAdminBootstrapNotAllowed, got %v", err)
	}
}

func TestAdminBootstrapSuccess(t *testing.T) {
	cfg := config.LoadConfig()
	security.SetRuntimeMasterKey(make([]byte, 32))
	defer security.SetRuntimeMasterKey(nil)

	repo := &adminBootstrapRepoMock{
		acquireFn: func(ctx context.Context) (iamRepoInterface.BootstrapLock, error) { return bootstrapLockMock{}, nil },
		activeFn:  func(ctx context.Context) (*iamEntity.AdminAPIKey, error) { return nil, nil },
		bootstrapFn: func(ctx context.Context, payload iamEntity.AdminBootstrapPayload) (time.Time, error) {
			if len(payload.RecoveryCodeHashes) != 8 {
				t.Fatalf("expected 8 recovery hashes, got %d", len(payload.RecoveryCodeHashes))
			}
			if payload.KeyHash == "" || payload.SecretCiphertext == "" {
				t.Fatalf("expected key hash and secret ciphertext")
			}
			return time.Now().UTC(), nil
		},
		rollbackFn: func(ctx context.Context, payload iamEntity.AdminBootstrapPayload) error { return nil },
	}
	svc := iamSvcImpl.NewAdminAPIKeyService(cfg, repo, telegram.NewTelegramClient("", ""), nil, nil, nil, nil)
	err := svc.Bootstrap(context.Background(), "tester")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

}

func (m *adminBootstrapRepoMock) GetAdmin2FASettings(ctx context.Context) (*iamEntity.Admin2FASettings, error) {
	if m.get2FAFn != nil {
		return m.get2FAFn(ctx)
	}
	return nil, nil
}

func (m *adminBootstrapRepoMock) ConsumeRecoveryCode(ctx context.Context, codeHash string, now time.Time) (bool, error) {
	if m.consumeRecoveryFn != nil {
		return m.consumeRecoveryFn(ctx, codeHash, now)
	}
	return false, nil
}

func (m *adminBootstrapRepoMock) UpsertAdminDeviceBinding(ctx context.Context, input iamEntity.AdminDeviceBindingInput) (*iamEntity.AdminDevice, error) {
	if m.upsertDeviceFn != nil {
		return m.upsertDeviceFn(ctx, input)
	}
	return &iamEntity.AdminDevice{}, nil
}

func (m *adminBootstrapRepoMock) GetAdminDeviceByID(ctx context.Context, deviceID string) (*iamEntity.AdminDevice, error) {
	if m.getDeviceByIDFn != nil {
		return m.getDeviceByIDFn(ctx, deviceID)
	}
	return nil, nil
}

func (m *adminBootstrapRepoMock) TouchAdminDeviceLastSeen(ctx context.Context, deviceID string, ip *string, userAgent *string, seenAt time.Time) error {
	if m.touchLastSeenFn != nil {
		return m.touchLastSeenFn(ctx, deviceID, ip, userAgent, seenAt)
	}
	return nil
}
