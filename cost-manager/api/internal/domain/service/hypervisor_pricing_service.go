package billingSvcInterface

import (
	"context"
	"cost-manager/api/internal/domain/entity"

	"github.com/google/uuid"
)

type HypervisorPricingService interface {
	EstimateHypervisor(context.Context, int64, int64, int64, uuid.UUID) (*entity.HypervisorEstimate, error)
	RunPricingCacheInvalidation(context.Context)
	RunPricingSnapshotRefresh(context.Context)
	RunPricingOutboxRelay(context.Context)
	NotifyPricingOutbox()
	GetHypervisorBasePricePublishTarget(context.Context, string) (*entity.HypervisorBasePricePublishTarget, error)
	CreateHypervisorBasePriceVersion(context.Context, entity.HypervisorBasePricePublishCommand, []entity.HypervisorBasePriceBracketCommand) (*entity.HypervisorBasePricePublished, error)
	CreateHypervisorZonePriceAdjustment(context.Context, entity.HypervisorZoneAdjustmentPublishCommand) (*entity.HypervisorZoneAdjustmentPublished, error)
}
