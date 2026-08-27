package billingRepoInterface

import (
	"context"
	"cost-manager/api/internal/domain/entity"
	"time"

	"github.com/google/uuid"
)

type HypervisorPricingRepository interface {
	GetActiveHypervisorPricingSnapshot(context.Context, entity.ChargeKindCode, time.Time) (*entity.HypervisorPricingSnapshot, error)
	GetHypervisorBasePricePublishTarget(context.Context, string) (*entity.HypervisorBasePricePublishTarget, error)
	CreateHypervisorBasePriceVersion(context.Context, entity.HypervisorBasePricePublishCommand, []entity.HypervisorBasePriceBracketCommand) (*entity.HypervisorBasePricePublished, error)
	GetActiveHypervisorZonePriceAdjustment(context.Context, uuid.UUID, time.Time) (*entity.HypervisorZoneAdjustmentSnapshot, error)
	CreateHypervisorZonePriceAdjustment(context.Context, entity.HypervisorZoneAdjustmentPublishCommand) (*entity.HypervisorZoneAdjustmentPublished, error)
	ListHypervisorZonePriceAdjustments(context.Context, entity.HypervisorZoneAdjustmentListQuery) ([]entity.HypervisorZoneAdjustmentListItem, bool, error)
	RefreshHypervisorPricingStatuses(context.Context) error
	ClaimHypervisorPricingOutbox(context.Context, uuid.UUID, time.Time, int) ([]*entity.PricingOutboxRow, error)
	MarkHypervisorPricingOutboxPublished(context.Context, uuid.UUID, uuid.UUID) error
	RetryHypervisorPricingOutbox(context.Context, uuid.UUID, uuid.UUID, string, time.Time) error
}
