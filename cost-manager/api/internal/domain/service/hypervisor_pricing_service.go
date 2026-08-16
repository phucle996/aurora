package billingSvcInterface

import (
	"context"
	"cost-manager/api/internal/domain/entity"

	"github.com/google/uuid"
)

type HypervisorEstimateService interface {
	EstimateHypervisor(context.Context, int64, int64, int64, uuid.UUID) (*entity.HypervisorEstimate, error)
	RunPricingCacheInvalidation(context.Context)
	RunPricingReadinessProjection(context.Context)
}

type HypervisorZoneAdjustmentPublishService interface {
	CreateHypervisorZonePriceAdjustment(context.Context, entity.HypervisorZoneAdjustmentPublishCommand) (*entity.HypervisorZoneAdjustmentPublished, error)
}
