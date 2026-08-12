package billingRepoInterface

import (
	"context"
	"cost-manager/api/internal/domain/entity"
	"time"

	"github.com/google/uuid"
)

// PricingScheduleRepository owns the controlled schedule catalog and immutable
// version transaction. It never resolves owners or mutates wallets.
type PricingScheduleRepository interface {
	ListPricingSchedules(ctx context.Context, page, limit int, chargeKind entity.ChargeKindCode, search string) ([]*entity.PricingSchedule, int64, error)
	GetPricingScheduleDetail(ctx context.Context, code string) (*entity.PricingScheduleDetail, error)
	GetActivePricingSnapshot(ctx context.Context, chargeKind entity.ChargeKindCode, zoneID *uuid.UUID, at time.Time) (*entity.PricingSnapshot, error)
	UpdatePricingScheduleMetadata(ctx context.Context, update entity.PricingScheduleMetadataUpdate) (*entity.PricingSchedule, error)
	CreatePricingScheduleVersion(ctx context.Context, create entity.PricingScheduleVersionCreate) (*entity.PricingScheduleVersion, error)
}
