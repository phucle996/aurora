package hierarchyRepoInterface

import (
	"context"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
)

type TenantRepository interface {
	CreateTenant(context.Context, *hierarchyEntity.CreateTenant) (*hierarchyEntity.CreateTenant, error)
}
