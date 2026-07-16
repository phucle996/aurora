package repo

import (
	"context"

	"cost-manager/api/internal/domain/entity"
)

type PriceRepository interface {
	// ListPrices trả về tất cả đơn giá cấu hình (bao gồm metric_type, unit, free_quota)
	ListPrices(ctx context.Context) ([]entity.Price, error)

	// GetPriceByMetric lấy đơn giá đang hiệu lực theo (service_type, metric_type, zone_code, tier)
	// Dùng bởi billing service để tính overage khi usage vượt quota subscription
	GetPriceByMetric(ctx context.Context, serviceType, metricType, zoneCode, tier string) (*entity.Price, error)

	// CreateOrUpdatePrice upsert 1 đơn giá, conflict theo unique (service_type, metric_type, zone_code, tier)
	CreateOrUpdatePrice(ctx context.Context, p *entity.Price) error
}
