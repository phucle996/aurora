package repo

import (
	"context"

	"cost-manager/api/internal/domain/entity"
	"github.com/google/uuid"
)

type WalletRepository interface {
	GetOrCreateWallet(ctx context.Context, ownerID uuid.UUID, ownerType string) (*entity.Wallet, error)
	Deposit(ctx context.Context, ownerID uuid.UUID, ownerType string, amount float64, desc string) error
	GetTransactions(ctx context.Context, walletID uuid.UUID) ([]entity.Transaction, error)

	// Debit trừ tiền từ ví theo walletID (không phải ownerID) với SELECT FOR UPDATE để tránh race condition.
	// txType: 'SUBSCRIPTION_FEE' | 'USAGE_CHARGE' | 'OVERAGE_CHARGE'
	Debit(ctx context.Context, walletID uuid.UUID, amount float64, txType, serviceType, desc string) error
}

