/*
============================================================================
MAP: BILLING DOMAIN REPOSITORY INTERFACE - LIFECYCLE REPOSITORY
============================================================================
CONTRACT:
1. Định nghĩa interface thực thi SQL cho Inbox, Head Version và Resource Ownership Projection.
============================================================================
*/

package billingRepoInterface

import (
	"context"
	"cost-manager/api/internal/domain/entity"
)

// [COMMENT]: LifecycleRepository quản lý persistence cho sự kiện vòng đời tài nguyên.
type LifecycleRepository interface {
	ApplyLifecycleEvent(ctx context.Context, event *entity.LifecycleEvent) error
}
