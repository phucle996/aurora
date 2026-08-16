package billingRepoInterface

import (
	"context"
	"cost-manager/api/internal/domain/entity"
	"time"

	"github.com/google/uuid"
)

type MailZoneAdjustmentRepository interface {
	GetActiveMailZonePriceAdjustment(context.Context, uuid.UUID, time.Time) (*entity.MailZoneAdjustmentSnapshot, error)
	CreateMailZonePriceAdjustment(context.Context, entity.MailZoneAdjustmentPublishCommand) (*entity.MailZoneAdjustmentPublished, error)
}
