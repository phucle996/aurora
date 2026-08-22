package billingRepoInterface

import (
	"context"
	"time"

	"cost-manager/api/internal/domain/entity"

	"github.com/google/uuid"
)

// StoragePricingRepository owns every Storage pricing persistence workflow.
// Global base rows stay in the common catalog tables, but only this port can
// mutate rows whose controlled charge kind belongs to Storage.
type StoragePricingRepository interface {
	GetActiveStoragePricingSnapshot(ctx context.Context, chargeKind entity.ChargeKindCode, at time.Time) (*entity.StoragePricingSnapshot, error)
	GetStorageBasePricePublishTarget(ctx context.Context, code string) (*entity.StorageBasePricePublishTarget, error)
	CreateStorageBasePriceVersion(ctx context.Context, create entity.StorageBasePricePublishCommand, brackets []entity.StorageBasePricePublishBracket) (*entity.StorageBasePricePublished, error)
	GetActiveStorageZonePriceAdjustment(ctx context.Context, zoneID uuid.UUID, at time.Time) (*entity.StorageZoneAdjustmentSnapshot, error)
	CreateStorageZonePriceAdjustment(ctx context.Context, create entity.StorageZoneAdjustmentPublishCommand) (*entity.StorageZoneAdjustmentPublished, error)
	ListStorageZonePriceAdjustments(ctx context.Context, query entity.StorageZoneAdjustmentListQuery) ([]entity.StorageZoneAdjustmentListItem, bool, error)
	RefreshStoragePricingStatuses(ctx context.Context) error
	ClaimStoragePricingOutbox(ctx context.Context, claimToken uuid.UUID, leaseUntil time.Time, limit int) ([]*entity.PricingOutboxRow, error)
	MarkStoragePricingOutboxPublished(ctx context.Context, id, claimToken uuid.UUID) error
	RetryStoragePricingOutbox(ctx context.Context, id, claimToken uuid.UUID, lastError string, availableAt time.Time) error
}
