package billingSvcInterface

import (
	"context"

	"cost-manager/api/internal/domain/entity"

	"github.com/google/uuid"
)

type PersonalPaymentService interface {
	GetWallet(ctx context.Context, ownerID uuid.UUID) (*entity.WalletSummary, error)
	CreateTopUp(ctx context.Context, command entity.CreatePersonalPaymentIntentCommand) (*entity.PaymentIntent, error)
	GetTopUp(ctx context.Context, ownerID uuid.UUID, intentID uuid.UUID) (*entity.PaymentIntent, error)
	ApplyVerifiedSettlement(ctx context.Context, settlement entity.PaymentSettlement) (*entity.SettlementResult, error)
}

type TenantPaymentService interface {
	GetWallet(ctx context.Context, tenantID uuid.UUID) (*entity.WalletSummary, error)
	CreateTopUp(ctx context.Context, command entity.CreateTenantPaymentIntentCommand) (*entity.PaymentIntent, error)
	GetTopUp(ctx context.Context, tenantID uuid.UUID, intentID uuid.UUID) (*entity.PaymentIntent, error)
	ApplyVerifiedSettlement(ctx context.Context, settlement entity.PaymentSettlement) (*entity.SettlementResult, error)
}
