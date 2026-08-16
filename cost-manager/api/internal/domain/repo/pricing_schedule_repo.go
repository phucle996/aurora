package billingRepoInterface

import (
	"context"
	"cost-manager/api/internal/domain/entity"
	"time"

	"github.com/google/uuid"
)

type PricingScheduleListRepository interface {
	ListPricingSchedules(context.Context, int, int, entity.ChargeKindCode, string) ([]*entity.PricingScheduleListItem, int64, error)
}

type PricingScheduleDetailRepository interface {
	GetPricingScheduleDetail(context.Context, string) (*entity.PricingScheduleDetail, []entity.PricingScheduleDetailBracket, error)
}

type PricingSnapshotRepository interface {
	GetActivePricingSnapshot(context.Context, entity.ChargeKindCode, time.Time) (*entity.PricingSnapshot, error)
}

type PricingScheduleMetadataRepository interface {
	UpdatePricingScheduleMetadata(context.Context, entity.PricingScheduleMetadataCommand) (*entity.PricingScheduleMetadataUpdated, error)
}

type PricingScheduleVersionPublishRepository interface {
	GetPricingScheduleVersionPublishTarget(context.Context, string) (*entity.PricingScheduleVersionPublishTarget, error)
	CreatePricingScheduleVersion(context.Context, entity.PricingScheduleVersionPublishCommand, []entity.PricingScheduleVersionPublishBracket) (*entity.PricingScheduleVersionPublished, error)
}

type StorageZoneAdjustmentRepository interface {
	GetActiveStorageZonePriceAdjustment(context.Context, uuid.UUID, time.Time) (*entity.StorageZoneAdjustmentSnapshot, error)
	CreateStorageZonePriceAdjustment(context.Context, entity.StorageZoneAdjustmentPublishCommand) (*entity.StorageZoneAdjustmentPublished, error)
}
