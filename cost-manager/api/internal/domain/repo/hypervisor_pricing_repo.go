package billingRepoInterface

import (
	"context"
	"cost-manager/api/internal/domain/entity"
	"time"

	"github.com/google/uuid"
)

type HypervisorZoneAdjustmentRepository interface {
	GetActiveHypervisorZonePriceAdjustment(context.Context, uuid.UUID, time.Time) (*entity.HypervisorZoneAdjustmentSnapshot, error)
	CreateHypervisorZonePriceAdjustment(context.Context, entity.HypervisorZoneAdjustmentPublishCommand) (*entity.HypervisorZoneAdjustmentPublished, error)
}
