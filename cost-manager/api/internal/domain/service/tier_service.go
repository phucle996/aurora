package billingSvcInterface

import (
	"context"
	"cost-manager/api/internal/domain/entity"
)

// [COMMENT]: TierService định nghĩa luồng xử lý nghiệp vụ liên quan tới Tiers.
type TierService interface {
	// GetTiersList điều hướng gọi repository để trả về danh sách cước phân trang.
	GetTiersList(ctx context.Context, page, limit int, serviceType entity.ServiceType, search string) ([]*entity.Tier, int64, error)
	// GetTierDetail trả full latest aggregate, không phụ thuộc flat pagination.
	GetTierDetail(ctx context.Context, code string, serviceType entity.ServiceType) (*entity.TierDetail, error)
	EstimateStorage(ctx context.Context, capacityBytes int64) (*entity.StorageEstimate, error)
	RunPricingCacheInvalidation(ctx context.Context)
	// UpdateTierMetadata sửa display name mà không tác động pricing version.
	UpdateTierMetadata(ctx context.Context, update entity.TierMetadataUpdate) (*entity.TierMetadata, error)
	// CreateTierVersion validate và publish immutable pricing snapshot.
	CreateTierVersion(ctx context.Context, create entity.TierVersionCreate) (*entity.TierVersion, error)
}
