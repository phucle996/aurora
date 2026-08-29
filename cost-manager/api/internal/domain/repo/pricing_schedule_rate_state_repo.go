package billingRepoInterface

import (
	"context"

	"cost-manager/api/internal/domain/entity"
)

// PricingScheduleRateStateRepository owns the operator read which separates the rate
// currently effective from the next scheduled rate. It never mutates pricing.
type PricingScheduleRateStateRepository interface {
	GetPricingScheduleRateState(context.Context, string) ([]entity.PricingScheduleRateStateRow, error)
}
