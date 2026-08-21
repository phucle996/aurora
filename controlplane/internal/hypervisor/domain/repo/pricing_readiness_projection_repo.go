package hypervisorRepoInterface

import (
	"context"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
)

type PricingReadinessProjectionWriter interface {
	UpsertPricingReadiness(context.Context, *hypervisorEntity.PricingReadinessProjection) error
}

type PricingReadinessProjectionReader interface {
	ReadPricingReadiness(context.Context) (*hypervisorEntity.PricingReadinessProjection, error)
}
