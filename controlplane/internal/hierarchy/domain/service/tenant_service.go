package hierarchySvcInterface

import (
	"context"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
)

type TenantService interface {
	CreateTenant(context.Context, *hierarchyEntity.CreateTenant) (*hierarchyEntity.CreateTenant, error)
}
