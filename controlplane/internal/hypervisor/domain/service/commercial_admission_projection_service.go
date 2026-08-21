package hypervisorSvcInterface

import (
	"context"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
)

type CommercialAdmissionProjectionService interface {
	Apply(context.Context, *hypervisorEntity.CommercialAdmissionProjectionCommand) error
}
