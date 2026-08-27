package billingRepoInterface

import (
	"context"
	"time"

	"cost-manager/api/internal/domain/entity"

	"github.com/google/uuid"
)

type HypervisorResourcePlanRepository interface {
	ListPlans(context.Context, entity.HypervisorResourcePlanAdminQuery) ([]entity.HypervisorResourcePlanAdminItem, bool, error)
	ListRevisions(context.Context, entity.HypervisorResourcePlanHistoryQuery) ([]entity.HypervisorResourcePlanHistoryItem, bool, error)
	GetHypervisorResourcePlanIdentity(context.Context, uuid.UUID) (*entity.HypervisorResourcePlanRevision, error)
	CreateHypervisorResourcePlan(context.Context, entity.CreateHypervisorResourcePlanCommand) (*entity.HypervisorResourcePlanRevision, error)
	PublishHypervisorResourcePlanRevision(context.Context, entity.PublishHypervisorResourcePlanRevisionCommand) (*entity.HypervisorResourcePlanRevision, error)
	ListEffectiveHypervisorResourcePlans(context.Context, entity.HypervisorResourcePlanListQuery) ([]entity.HypervisorResourcePlanRevision, bool, error)
	ClaimHypervisorResourcePlanOutbox(context.Context, uuid.UUID, time.Time, int) ([]entity.HypervisorResourcePlanOutboxRow, error)
	MarkHypervisorResourcePlanOutboxPublished(context.Context, uuid.UUID, uuid.UUID) error
	RetryHypervisorResourcePlanOutbox(context.Context, uuid.UUID, uuid.UUID, string, time.Time) error
}
