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
}
