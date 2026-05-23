package coreSvcInterface

import (
	"context"
	"time"

	coreEntity "controlplane/internal/core/domain/entity"
)

type SecretReadService interface {
	GetRuntimeSecretFamily(ctx context.Context, familyCode string) (*coreEntity.RuntimeSecretFamily, error)
}

type RuntimeSecretProvider interface {
	GetPrimary(ctx context.Context, familyCode string) (*coreEntity.RuntimeSecret, error)
	GetCandidates(ctx context.Context, familyCode string) ([]coreEntity.RuntimeSecret, error)
	Warm(ctx context.Context, familyCode string) error
	Invalidate(familyCode string)
}

type SecretRotationService interface {
	PlanRotation(ctx context.Context, familyCode string, ttl time.Duration) (*coreEntity.RotationPlan, error)
	EnsureInitialSecretVersion(ctx context.Context, family coreEntity.BootstrapSecretFamily) (*coreEntity.EnsureInitialSecretResult, error)
	RotateSecretFamily(ctx context.Context, input coreEntity.RotateSecretFamilyInput) (*coreEntity.SecretVersion, error)
}
