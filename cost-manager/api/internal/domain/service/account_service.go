package billingSvcInterface

import (
	"context"

	"cost-manager/api/internal/domain/entity"
	"github.com/google/uuid"
)

// [COMMENT]: AccountService cung cấp activation contract không cho caller chọn campaign hoặc số tiền.
type AccountService interface {
	ActivatePersonalFreeTier(ctx context.Context, ownerID string, idempotencyKey string) (*entity.FreeTierAccount, error)
	ProvisionPersonalWallet(ctx context.Context, eventID uuid.UUID, ownerID uuid.UUID, payloadHash string) error
	GetPersonalWalletSummary(ctx context.Context, ownerID uuid.UUID) (*entity.WalletSummary, error)
}
