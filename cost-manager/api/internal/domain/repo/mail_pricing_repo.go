package billingRepoInterface

import (
	"context"
	"cost-manager/api/internal/domain/entity"
	"time"

	"github.com/google/uuid"
)

type MailPricingRepository interface {
	GetActiveMailPricingSnapshot(context.Context, entity.ChargeKindCode, time.Time) (*entity.MailPricingSnapshot, error)
	GetMailBasePricePublishTarget(context.Context, string) (*entity.MailBasePricePublishTarget, error)
	CreateMailBasePriceVersion(context.Context, entity.MailBasePricePublishCommand, []entity.MailBasePriceBracketCommand) (*entity.MailBasePricePublished, error)
	GetActiveMailZonePriceAdjustment(context.Context, uuid.UUID, time.Time) (*entity.MailZoneAdjustmentSnapshot, error)
	CreateMailZonePriceAdjustment(context.Context, entity.MailZoneAdjustmentPublishCommand) (*entity.MailZoneAdjustmentPublished, error)
	ListMailZonePriceAdjustments(context.Context, entity.MailZoneAdjustmentListQuery) ([]entity.MailZoneAdjustmentListItem, bool, error)
	RefreshMailPricingStatuses(context.Context) error
	ClaimMailPricingOutbox(context.Context, uuid.UUID, time.Time, int) ([]*entity.PricingOutboxRow, error)
	MarkMailPricingOutboxPublished(context.Context, uuid.UUID, uuid.UUID) error
	RetryMailPricingOutbox(context.Context, uuid.UUID, uuid.UUID, string, time.Time) error
}
