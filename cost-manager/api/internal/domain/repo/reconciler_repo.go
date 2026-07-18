/*
============================================================================
MAP: BILLING DOMAIN REPOSITORY INTERFACE - RECONCILER
============================================================================
CONTRACT:
1. Quản lý PostgreSQL Advisory Lock phục vụ HA Leader Election.
2. Truy vấn và cập nhật trạng thái đối soát của billing.resource_ownership_projection.
============================================================================
*/

package billingRepoInterface

import (
	"context"
	"cost-manager/api/internal/domain/entity"
)

// [COMMENT]: ReconcilerRepository định nghĩa contract thực thi SQL queries đối soát sở hữu.
type ReconcilerRepository interface {
	TryAdvisoryLock(ctx context.Context, lockID int64) (bool, error)
	AdvisoryUnlock(ctx context.Context, lockID int64) error
	GetUnreconciledProjections(ctx context.Context, limit int) ([]*entity.UnreconciledProjection, error)
	MarkReconciledBatch(ctx context.Context) error
}
