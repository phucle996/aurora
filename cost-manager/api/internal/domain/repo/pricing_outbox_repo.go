/*
============================================================================
MAP: BILLING DOMAIN REPOSITORY INTERFACE - PRICING OUTBOX
============================================================================
CONTRACT:
1. Định nghĩa interface thực thi các truy vấn SQL cho bảng billing.pricing_outbox và billing.pricing_schedule_versions.
============================================================================
*/

package billingRepoInterface

import (
	"context"

	"cost-manager/api/internal/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: PricingOutboxRepository định nghĩa các contract SQL phục vụ việc relay
// hint bảng giá sang Shared Redis; PostgreSQL vẫn là pricing SoT.
type PricingOutboxRepository interface {
	RefreshPricingScheduleVersionStatuses(ctx context.Context) error
	GetUnpublishedOutboxBatch(ctx context.Context, limit int) ([]*entity.PricingOutboxRow, error)
	MarkOutboxPublished(ctx context.Context, id uuid.UUID) error
	RecordOutboxError(ctx context.Context, id uuid.UUID, errMsg string) error
}
