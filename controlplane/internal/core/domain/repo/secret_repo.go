package coreRepoInterface

import (
	"context"

	coreEntity "controlplane/internal/core/domain/entity"
)

type SecretBootstrapLock interface {
	Release(ctx context.Context) error
}

type SecretRotationLock interface {
	Release(ctx context.Context) error
}

type SecretRepository interface {
	AcquireSecretTypeBootstrapLock(ctx context.Context, secretType string) (SecretBootstrapLock, error)
	AcquireSecretTypeRotationLock(ctx context.Context, secretType string) (SecretRotationLock, error)
	GetSecretsByType(ctx context.Context, secretType string) (*coreEntity.RuntimeSecrets, error)
	SaveSecrets(ctx context.Context, row coreEntity.CoreSecretRow) error
	UpdateSecrets(ctx context.Context, secretType string, activeSecret, activeFingerprint string, standbySecret, standbyFingerprint string) error
	GetAccessSecret(ctx context.Context) (*coreEntity.RuntimeSecrets, error)
	GetRefreshSecret(ctx context.Context) (*coreEntity.RuntimeSecrets, error)
	GetAdminAPIKey(ctx context.Context) (*coreEntity.RuntimeSecrets, error)
	GetOneTimeTokenSecret(ctx context.Context) (*coreEntity.RuntimeSecrets, error)
}
