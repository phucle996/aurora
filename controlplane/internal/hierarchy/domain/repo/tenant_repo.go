package hierarchyRepoInterface

import (
	"context"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	"github.com/google/uuid"
)

type TenantRepository interface {
	CreateTenant(context.Context, *hierarchyEntity.CreateTenant) (*hierarchyEntity.CreateTenant, error)
	ListTenantsForUser(context.Context, uuid.UUID) ([]hierarchyEntity.TenantCatalogItem, error)
}
