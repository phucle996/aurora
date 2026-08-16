package billingRepoInterface

import (
	"context"

	"cost-manager/api/internal/domain/entity"
)

type MailZoneAdjustmentListRepository interface {
	ListMailZonePriceAdjustments(context.Context, entity.MailZoneAdjustmentListQuery) ([]entity.MailZoneAdjustmentListItem, bool, error)
}
