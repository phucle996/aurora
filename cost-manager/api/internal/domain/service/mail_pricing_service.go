package billingSvcInterface

import (
	"context"
	"cost-manager/api/internal/domain/entity"

	"github.com/google/uuid"
)

type MailPricingService interface {
	EstimateMail(context.Context, int64, uuid.UUID) (*entity.MailEstimate, error)
	RunPricingCacheInvalidation(context.Context)
	RunPricingSnapshotRefresh(context.Context)
	RunPricingOutboxRelay(context.Context)
	NotifyPricingOutbox()
	GetMailBasePricePublishTarget(context.Context, string) (*entity.MailBasePricePublishTarget, error)
	CreateMailBasePriceVersion(context.Context, entity.MailBasePricePublishCommand, []entity.MailBasePriceBracketCommand) (*entity.MailBasePricePublished, error)
	CreateMailZonePriceAdjustment(context.Context, entity.MailZoneAdjustmentPublishCommand) (*entity.MailZoneAdjustmentPublished, error)
	ListMailZonePriceAdjustments(context.Context, entity.MailZoneAdjustmentListQuery) (*entity.MailZoneAdjustmentListResult, error)
}
