package billingSvcInterface

import (
	"context"

	"cost-manager/api/internal/domain/entity"
)

type MailZoneAdjustmentListService interface {
	ListMailZonePriceAdjustments(context.Context, entity.MailZoneAdjustmentListQuery) (*entity.MailZoneAdjustmentListResult, error)
}
