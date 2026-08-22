package hierarchyRepoInterface

import (
	"context"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	"github.com/google/uuid"
)

type TenantRepository interface {
	CreateTenant(context.Context, *hierarchyEntity.CreateTenant) (*hierarchyEntity.CreateTenant, error)
	ListTenantsForUser(context.Context, uuid.UUID) ([]hierarchyEntity.TenantCatalogItem, error)
	ClaimTenantWalletProvisionOutbox(context.Context, int) ([]hierarchyEntity.TenantWalletProvisionOutbox, error)
	MarkTenantWalletProvisionPublished(context.Context, int64) error
	MarkTenantWalletProvisionFailed(context.Context, int64, string) error
	MarkTenantWalletProvisionDead(context.Context, int64, string) error
}
