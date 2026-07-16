package service

import (
	"context"
	"fmt"

	"cost-manager/api/internal/domain/entity"
	domainrepo "cost-manager/api/internal/domain/repo"
	domainservice "cost-manager/api/internal/domain/service"
	"cost-manager/api/pkg/apperr"
	"github.com/google/uuid"
)

type BillingServiceImpl struct {
	walletRepo domainrepo.WalletRepository
	priceRepo  domainrepo.PriceRepository
	subRepo    domainrepo.SubscriptionRepository // cần để check quota
}

func NewBillingService(
	walletRepo domainrepo.WalletRepository,
	priceRepo domainrepo.PriceRepository,
	subRepo domainrepo.SubscriptionRepository,
) domainservice.BillingService {
	return &BillingServiceImpl{
		walletRepo: walletRepo,
		priceRepo:  priceRepo,
		subRepo:    subRepo,
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

// ChargeUsage được Rust Engine gọi sau mỗi chu kỳ đo lường.
// Flow:
//  1. Lấy subscription active của owner
//  2. Nếu có subscription → tìm metric quota tương ứng
//     - Nếu usageAmount ≤ quota → miễn phí (trong gói), return nil
//     - Nếu usageAmount > quota → tính overage cho phần vượt
//  3. Nếu không có subscription → tính toàn bộ theo đơn giá (pay-as-you-go)
//     - Trừ free_quota nếu có
func (s *BillingServiceImpl) ChargeUsage(ctx context.Context, ownerID uuid.UUID, ownerType string, metricType string, usageAmount float64) error {
	const op = "service.billing.charge_usage"

	// Lấy wallet (tạo nếu chưa có)
	wallet, err := s.walletRepo.GetOrCreateWallet(ctx, ownerID, ownerType)
	if err != nil {
		return fmt.Errorf("%s: get wallet: %w", op, err)
	}

	// Lấy subscription active
	sub, err := s.subRepo.GetActiveSubscription(ctx, ownerID, ownerType)
	if err != nil {
		return fmt.Errorf("%s: get subscription: %w", op, err)
	}

	chargeableAmount := usageAmount

	// Nếu có subscription, kiểm tra quota
	if sub != nil && sub.Plan != nil {
		for _, m := range sub.Plan.Metrics {
			if string(m.MetricType) == metricType {
				if usageAmount <= m.Quota {
					// Trong quota — miễn phí, không trừ ví
					return nil
				}
				// Vượt quota → chỉ tính phần overage
				chargeableAmount = usageAmount - m.Quota
				break
			}
		}
	}

	// Lấy đơn giá (service_type=STORAGE mặc định, zone=global nếu không có)
	price, err := s.priceRepo.GetPriceByMetric(ctx, "STORAGE", metricType, "global", "STANDARD")
	if err != nil {
		// Thử lại với zone cụ thể nếu global không tồn tại
		price, err = s.priceRepo.GetPriceByMetric(ctx, "STORAGE", metricType, string(wallet.OwnerType), "STANDARD")
		if err != nil {
			return apperr.Wrap(apperr.ErrPriceNotFound,
				fmt.Errorf("%s: price not found for metric %s", op, metricType), "price_not_found")
		}
	}

	// Trừ free_quota pay-as-you-go nếu không có subscription
	if sub == nil && price.FreeQuota > 0 {
		if chargeableAmount <= price.FreeQuota {
			return nil
		}
		chargeableAmount -= price.FreeQuota
	}

	totalCost := chargeableAmount * price.UnitPrice
	if totalCost <= 0 {
		return nil
	}

	// Trừ ví với SELECT FOR UPDATE
	txType := "USAGE_CHARGE"
	if sub != nil {
		txType = "OVERAGE_CHARGE"
	}
	desc := fmt.Sprintf("Usage charge: %.4f %s (%.6f/unit)", chargeableAmount, price.Unit, price.UnitPrice)
	return s.walletRepo.Debit(ctx, wallet.ID, totalCost, txType, "STORAGE", desc)
}
