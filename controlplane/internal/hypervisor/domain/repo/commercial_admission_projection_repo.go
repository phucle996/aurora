package hypervisorRepoInterface

import (
	"context"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
)

type CommercialAdmissionProjectionRepository interface {
	Upsert(context.Context, *hypervisorEntity.CommercialAdmissionProjection) error
}
