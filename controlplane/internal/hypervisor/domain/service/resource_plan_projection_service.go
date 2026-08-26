package hypervisorSvcInterface

import (
	"context"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
)

type HypervisorResourcePlanProjectionService interface {
	Apply(context.Context, *hypervisorEntity.HypervisorResourcePlanProjectionCommand) error
	RefreshCache(context.Context) error
}
