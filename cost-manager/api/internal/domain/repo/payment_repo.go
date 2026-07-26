package billingRepoInterface

import (
	"context"

	"cost-manager/api/internal/domain/entity"

	"github.com/google/uuid"
)

// PersonalPaymentRepository exposes only self-owner payment operations.
type PersonalPaymentRepository interface {
	GetPersonalWalletSummary(ctx context.Context, ownerID uuid.UUID) (*entity.WalletSummary, error)
	CreatePersonalIntent(ctx context.Context, command entity.CreatePersonalPaymentIntentCommand) (*entity.PaymentIntent, error)
	GetPersonalIntent(ctx context.Context, ownerID uuid.UUID, intentID uuid.UUID) (*entity.PaymentIntent, error)
	ApplyPersonalSettlement(ctx context.Context, settlement entity.PaymentSettlement) (*entity.SettlementResult, error)
}

// TenantPaymentRepository requires the tenant owner and human actor to remain
// distinct throughout checkout and immutable ledger audit.
type TenantPaymentRepository interface {
	GetTenantWalletSummary(ctx context.Context, tenantID uuid.UUID) (*entity.WalletSummary, error)
	CreateTenantIntent(ctx context.Context, command entity.CreateTenantPaymentIntentCommand) (*entity.PaymentIntent, error)
	GetTenantIntent(ctx context.Context, tenantID uuid.UUID, intentID uuid.UUID) (*entity.PaymentIntent, error)
	ApplyTenantSettlement(ctx context.Context, settlement entity.PaymentSettlement) (*entity.SettlementResult, error)
}
