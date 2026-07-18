package billingSvcInterface

import (
	"context"
	"cost-manager/api/internal/domain/entity"
)

// [COMMENT]: TierService định nghĩa luồng xử lý nghiệp vụ liên quan tới Tiers.
type TierService interface {
	// GetTiersList điều hướng gọi repository để trả về danh sách cước phân trang.
	GetTiersList(ctx context.Context, page, limit int, serviceType, search string) ([]*entity.Tier, int64, error)
	// UpdateTier validate invariant của aggregate rồi thực hiện mutation nguyên tử.
	UpdateTier(ctx context.Context, update entity.TierUpdate) (*entity.TierAggregate, error)
}
