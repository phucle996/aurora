package billingSvcInterface

import (
	"context"
	"cost-manager/api/internal/domain/entity"
)

type PricingScheduleService interface {
	GetPricingSchedules(context.Context, int, int, entity.ChargeKindCode, string) ([]*entity.PricingScheduleListItem, int64, error)
	GetPricingScheduleDetail(context.Context, string) (*entity.PricingScheduleDetail, []entity.PricingScheduleDetailBracket, error)
	UpdatePricingScheduleMetadata(context.Context, entity.PricingScheduleMetadataCommand) (*entity.PricingScheduleMetadataUpdated, error)
}
