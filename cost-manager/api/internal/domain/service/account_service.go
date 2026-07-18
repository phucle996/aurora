package billingSvcInterface

import (
	"context"

	"cost-manager/api/internal/domain/entity"
)

// [COMMENT]: AccountService cung cấp activation contract không cho caller chọn campaign hoặc số tiền.
type AccountService interface {
	ActivatePersonalFreeTier(ctx context.Context, ownerID string, idempotencyKey string) (*entity.FreeTierAccount, error)
}
