package coreSvcInterface

import (
	"context"

	coreEntity "controlplane/internal/core/domain/entity"
)


type SecretRotationService interface {
	EnsureInitialSecrets(ctx context.Context, secretType string) (*coreEntity.RuntimeSecrets, error)
	RotateSecret(ctx context.Context, secretType string) (*coreEntity.RuntimeSecrets, error)
}
