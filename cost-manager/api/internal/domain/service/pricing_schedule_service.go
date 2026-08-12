package billingSvcInterface

import (
	"context"
	"cost-manager/api/internal/domain/entity"
	"time"

	"github.com/google/uuid"
)

type PricingScheduleService interface {
	GetPricingSchedules(ctx context.Context, page, limit int, chargeKind entity.ChargeKindCode, search string) ([]*entity.PricingSchedule, int64, error)
	GetPricingScheduleDetail(ctx context.Context, code string) (*entity.PricingScheduleDetail, error)
	EstimateStorage(ctx context.Context, capacityBytes int64, zoneID uuid.UUID) (*entity.StorageEstimate, error)
	RunPricingCacheInvalidation(ctx context.Context)
	UpdatePricingScheduleMetadata(ctx context.Context, update entity.PricingScheduleMetadataUpdate) (*entity.PricingSchedule, error)
	CreatePricingScheduleVersion(ctx context.Context, create entity.PricingScheduleVersionCreate) (*entity.PricingScheduleVersion, error)
	ResolveStorageSnapshot(ctx context.Context, zoneID uuid.UUID, at time.Time) (*entity.PricingSnapshot, error)
}
