package service

import (
	"context"

	"cost-manager/api/internal/domain/entity"
	"cost-manager/api/internal/domain/repo"
	"cost-manager/api/internal/domain/service"
	"github.com/google/uuid"
)

type BillingServiceImpl struct {
	walletRepo repo.WalletRepository
	priceRepo  repo.PriceRepository
}

func NewBillingService(walletRepo repo.WalletRepository, priceRepo repo.PriceRepository) service.BillingService {
	return &BillingServiceImpl{
		walletRepo: walletRepo,
		priceRepo:  priceRepo,
	}
}

func (s *BillingServiceImpl) GetOrCreateWallet(ctx context.Context, ownerID uuid.UUID, ownerType string) (*entity.Wallet, error) {
	return s.walletRepo.GetOrCreateWallet(ctx, ownerID, ownerType)
}

func (s *BillingServiceImpl) Deposit(ctx context.Context, ownerID uuid.UUID, ownerType string, amount float64, desc string) error {
	return s.walletRepo.Deposit(ctx, ownerID, ownerType, amount, desc)
}

func (s *BillingServiceImpl) GetTransactions(ctx context.Context, walletID uuid.UUID) ([]entity.Transaction, error) {
	return s.walletRepo.GetTransactions(ctx, walletID)
}

func (s *BillingServiceImpl) ListPrices(ctx context.Context) ([]entity.Price, error) {
	return s.priceRepo.ListPrices(ctx)
}

func (s *BillingServiceImpl) CreateOrUpdatePrice(ctx context.Context, p *entity.Price) error {
	return s.priceRepo.CreateOrUpdatePrice(ctx, p)
}
