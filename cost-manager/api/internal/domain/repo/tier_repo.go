package billingRepoInterface

import (
	"context"
	"cost-manager/api/internal/domain/entity"
)

// [COMMENT]: TierRepository định nghĩa các giao thức tương tác dữ liệu liên quan tới Tiers.
type TierRepository interface {
	// ListTiers lấy danh sách Tiers kèm theo Ranges tương ứng có phân trang và bộ lọc.
	ListTiers(ctx context.Context, page, limit int, serviceType, search string) ([]*entity.Tier, int64, error)
}
