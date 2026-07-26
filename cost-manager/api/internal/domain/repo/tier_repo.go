package billingRepoInterface

import (
	"context"
	"cost-manager/api/internal/domain/entity"
)

// [COMMENT]: TierRepository định nghĩa các giao thức tương tác dữ liệu liên quan tới Tiers.
type TierRepository interface {
	// ListTiers lấy danh sách Tiers kèm theo Ranges tương ứng có phân trang và bộ lọc.
	ListTiers(ctx context.Context, page, limit int, serviceType entity.ServiceType, search string) ([]*entity.Tier, int64, error)
	// GetTierDetail lấy full latest aggregate cho màn Edit.
	GetTierDetail(ctx context.Context, code string, serviceType entity.ServiceType) (*entity.TierDetail, error)
	GetActivePricingSnapshot(ctx context.Context, serviceType entity.ServiceType) (*entity.PricingSnapshot, error)
	// UpdateTierMetadata chỉ cập nhật name với OCC riêng cho metadata.
	UpdateTierMetadata(ctx context.Context, update entity.TierMetadataUpdate) (*entity.TierMetadata, error)
	// CreateTierVersion append immutable ranges và outbox trong cùng transaction.
	CreateTierVersion(ctx context.Context, create entity.TierVersionCreate) (*entity.TierVersion, error)
}
