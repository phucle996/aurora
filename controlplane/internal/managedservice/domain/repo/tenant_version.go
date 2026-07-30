package repo

import (
	"context"

	"controlplane/internal/managedservice/domain/entity"
)

type TenantCatalogVersionRepository interface {
	GetTenantCatalogVersion(context.Context, *entity.GetTenantCatalogVersion) (*entity.TenantCatalogVersionView, error)
}
