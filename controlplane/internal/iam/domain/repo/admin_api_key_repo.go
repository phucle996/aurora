package iamRepoInterface

import (
	"context"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"
)

type BootstrapLock interface {
	Release(ctx context.Context) error
}

type AdminAPIKeyRepository interface {
	AcquireBootstrapLock(ctx context.Context) (BootstrapLock, error)
	GetActiveAdminAPIKey(ctx context.Context) (*iamEntity.AdminAPIKey, error)
	GetAdmin2FASettings(ctx context.Context) (*iamEntity.Admin2FASettings, error)
	ConsumeRecoveryCode(ctx context.Context, codeHash string, now time.Time) (bool, error)
	GetAdminDeviceByID(ctx context.Context, deviceID string) (*iamEntity.AdminDevice, error)
	UpsertAdminDeviceBinding(ctx context.Context, input iamEntity.AdminDeviceBindingInput) (*iamEntity.AdminDevice, error)
	TouchAdminDeviceLastSeen(ctx context.Context, deviceID string, ip *string, userAgent *string, seenAt time.Time) error
	Bootstrap(ctx context.Context, payload iamEntity.AdminBootstrapPayload) (bootstrappedAt time.Time, err error)
	RollbackBootstrap(ctx context.Context, payload iamEntity.AdminBootstrapPayload) error
	AcquireRotationLock(ctx context.Context) (BootstrapLock, error)
	PrepareNextAdminAPIKey(ctx context.Context, actor string, keyHash string, expiresAt time.Time) error
	CommitPreparedAdminAPIKeyRotation(ctx context.Context) error
	RollbackPreparedAdminAPIKeyRotation(ctx context.Context) error
}
