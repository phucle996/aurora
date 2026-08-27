package hypervisorRepoInterface

import (
	"context"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
)

type HypervisorResourcePlanProjectionRepository interface {
	Insert(context.Context, *hypervisorEntity.HypervisorResourcePlanProjection) error
	ListEffective(context.Context) ([]hypervisorEntity.HypervisorResourcePlanProjection, error)
}
