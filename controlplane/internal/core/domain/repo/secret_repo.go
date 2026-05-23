package coreRepoInterface

import (
	"context"
	"time"

	coreEntity "controlplane/internal/core/domain/entity"
)

type SecretBootstrapLock interface {
	Release(ctx context.Context) error
}

type SecretRotationLock interface {
	Release(ctx context.Context) error
}

type SecretRepository interface {
	AcquireFamilyBootstrapLock(ctx context.Context, familyCode string) (SecretBootstrapLock, error)
	AcquireFamilyRotationLock(ctx context.Context, familyCode string) (SecretRotationLock, error)
	GetFamilyByCode(ctx context.Context, code string) (*coreEntity.SecretFamily, error)
	EnsureFamily(ctx context.Context, family coreEntity.SecretFamily) (*coreEntity.SecretFamily, error)
	ListVersionsByFamilyID(ctx context.Context, familyID string) ([]coreEntity.SecretVersion, error)
	CreateSecretVersion(ctx context.Context, version coreEntity.SecretVersion) error
	ReplacePrimaryVersion(ctx context.Context, familyID string, nextVersionID string, previousVersionID string, now time.Time) error
	RetireVersion(ctx context.Context, versionID string, retiredAt time.Time) error
	DeleteVersion(ctx context.Context, versionID string) error
	RotateFamilyVersions(ctx context.Context, familyID string, nextVersion coreEntity.SecretVersion, previousPrimaryID string, oldestVersionID string, retirePreviousNow bool, now time.Time) error
}
