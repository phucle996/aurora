package repo

import (
	"context"

	"cost-manager/api/internal/domain/entity"
)

type PriceRepository interface {
	ListPrices(ctx context.Context) ([]entity.Price, error)
	CreateOrUpdatePrice(ctx context.Context, p *entity.Price) error
}
