package billingSvcInterface

import (
	"context"
	"cost-manager/api/internal/domain/entity"

	"github.com/google/uuid"
)

type MailEstimateService interface {
	EstimateMail(context.Context, int64, uuid.UUID) (*entity.MailEstimate, error)
	RunPricingCacheInvalidation(context.Context)
	RunPricingReadinessProjection(context.Context)
}

type MailZoneAdjustmentPublishService interface {
	CreateMailZonePriceAdjustment(context.Context, entity.MailZoneAdjustmentPublishCommand) (*entity.MailZoneAdjustmentPublished, error)
}
