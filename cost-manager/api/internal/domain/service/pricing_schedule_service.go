package billingSvcInterface

import (
	"context"
	"cost-manager/api/internal/domain/entity"

	"github.com/google/uuid"
)

type PricingScheduleListService interface {
	GetPricingSchedules(context.Context, int, int, entity.ChargeKindCode, string) ([]*entity.PricingScheduleListItem, int64, error)
}

type PricingScheduleDetailService interface {
	GetPricingScheduleDetail(context.Context, string) (*entity.PricingScheduleDetail, []entity.PricingScheduleDetailBracket, error)
}

type StorageEstimateService interface {
	EstimateStorage(context.Context, int64, uuid.UUID) (*entity.StorageEstimate, error)
	RunPricingCacheInvalidation(context.Context)
}

type PricingScheduleMetadataService interface {
	UpdatePricingScheduleMetadata(context.Context, entity.PricingScheduleMetadataCommand) (*entity.PricingScheduleMetadataUpdated, error)
}

type PricingScheduleVersionPublishService interface {
	CreatePricingScheduleVersion(context.Context, entity.PricingScheduleVersionPublishCommand, []entity.PricingScheduleVersionPublishBracket) (*entity.PricingScheduleVersionPublished, []entity.PricingScheduleVersionPublishBracket, error)
}

type StorageZoneAdjustmentPublishService interface {
	CreateStorageZonePriceAdjustment(context.Context, entity.StorageZoneAdjustmentPublishCommand) (*entity.StorageZoneAdjustmentPublished, error)
}
