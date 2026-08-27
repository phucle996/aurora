package billingSvcInterface

import (
	"context"

	"cost-manager/api/internal/domain/entity"
)

type HypervisorResourcePlanService interface {
	ListPlans(context.Context, entity.HypervisorResourcePlanAdminQuery) ([]entity.HypervisorResourcePlanAdminItem, bool, error)
	ListRevisions(context.Context, entity.HypervisorResourcePlanHistoryQuery) ([]entity.HypervisorResourcePlanHistoryItem, bool, error)
	CreateHypervisorResourcePlan(context.Context, entity.CreateHypervisorResourcePlanCommand) (*entity.HypervisorResourcePlanRevision, error)
	PublishHypervisorResourcePlanRevision(context.Context, entity.PublishHypervisorResourcePlanRevisionCommand) (*entity.HypervisorResourcePlanRevision, error)
	ListEffectiveHypervisorResourcePlans(context.Context, entity.HypervisorResourcePlanListQuery) ([]entity.HypervisorResourcePlanRevision, bool, error)
	RunHypervisorResourcePlanOutboxRelay(context.Context)
	NotifyHypervisorResourcePlanOutbox()
}
