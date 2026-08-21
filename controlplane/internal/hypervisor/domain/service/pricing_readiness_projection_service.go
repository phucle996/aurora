package hypervisorSvcInterface

import (
	"context"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
)

type PricingReadinessProjectionService interface {
	ApplyPricingReadiness(context.Context, *hypervisorEntity.PricingReadinessProjectionCommand) error
}

type PricingReadinessGate interface {
	RequireHypervisorPricing(context.Context) error
}
