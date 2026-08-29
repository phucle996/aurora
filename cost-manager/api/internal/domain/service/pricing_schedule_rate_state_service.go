package billingSvcInterface

import (
	"context"

	"cost-manager/api/internal/domain/entity"
)

type PricingScheduleRateStateService interface {
	GetPricingScheduleRateState(context.Context, string) ([]entity.PricingScheduleRateStateRow, error)
}
