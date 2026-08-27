package billingSvcInterface

import (
	"context"

	"cost-manager/api/internal/domain/entity"

	"github.com/google/uuid"
)

// StoragePricingService là interface duy nhất quản lý toàn bộ nghiệp vụ giá cước và điều chỉnh giá Storage.
type StoragePricingService interface {
	EstimateStorage(ctx context.Context, capacityBytes int64, zoneID uuid.UUID) (*entity.StorageEstimate, error)
	RunPricingCacheInvalidation(ctx context.Context)
	RunPricingOutboxRelay(ctx context.Context)
	NotifyPricingOutbox()
	CreateStorageBasePriceVersion(ctx context.Context, create entity.StorageBasePricePublishCommand, brackets []entity.StorageBasePricePublishBracket) (*entity.StorageBasePricePublished, []entity.StorageBasePricePublishBracket, error)
	CreateStorageZonePriceAdjustment(ctx context.Context, create entity.StorageZoneAdjustmentPublishCommand) (*entity.StorageZoneAdjustmentPublished, error)
	ListStorageZonePriceAdjustments(ctx context.Context, query entity.StorageZoneAdjustmentListQuery) (*entity.StorageZoneAdjustmentListResult, error)
}
