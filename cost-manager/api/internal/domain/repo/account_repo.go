package billingRepoInterface

import (
	"context"

	"cost-manager/api/internal/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: AccountRepository sở hữu transaction boundary của subscription, wallet và ledger grant.
type AccountRepository interface {
	ActivateFreeTier(ctx context.Context, command entity.FreeTierActivation) (*entity.FreeTierAccount, error)
	ApplyPersonalWalletProvision(ctx context.Context, eventID uuid.UUID, ownerID uuid.UUID, payloadHash string) error
	GetPersonalWalletSummary(ctx context.Context, ownerID uuid.UUID) (*entity.WalletSummary, error)
}
