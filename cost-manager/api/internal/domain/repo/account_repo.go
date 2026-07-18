package billingRepoInterface

import (
	"context"

	"cost-manager/api/internal/domain/entity"
)

// [COMMENT]: AccountRepository sở hữu transaction boundary của subscription, wallet và ledger grant.
type AccountRepository interface {
	ActivateFreeTier(ctx context.Context, command entity.FreeTierActivation) (*entity.FreeTierAccount, error)
}
