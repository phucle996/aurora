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
	GetAdmin2FASecret(ctx context.Context) (secretCiphertext string, updatedAt time.Time, err error)

	// sau khi gọi consume recovery code thành công, ta cần đảm bảo rằng mã khôi phục này sẽ không được sử dụng lại, và nếu gọi lại lần nữa thì sẽ thất bại
	ConsumeRecoveryCode(ctx context.Context, codeHash string, now time.Time) error
	GetPublicKeyByDeviceID(ctx context.Context, deviceID string) (string, error)
	UpsertAdminDeviceBinding(ctx context.Context, input iamEntity.AdminDeviceBindingInput) (*iamEntity.AdminDevice, error)
	TouchAdminDeviceLastSeen(ctx context.Context, deviceID string, ip *string, userAgent *string, seenAt time.Time) error
	Bootstrap(ctx context.Context, payload iamEntity.AdminBootstrapPayload) (bootstrappedAt time.Time, err error)
	RollbackBootstrap(ctx context.Context, payload iamEntity.AdminBootstrapPayload) error
	AcquireRotationLock(ctx context.Context) (BootstrapLock, error)
	PrepareNextAdminAPIKey(ctx context.Context, key iamEntity.AdminAPIKey) error
}
