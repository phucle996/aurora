package repo

import (
	"context"

	"controlplane/internal/managedservice/domain/entity"
)

type TenantCatalogRepository interface {
	ListTenantCatalog(context.Context, *entity.ListTenantCatalog) (*entity.TenantCatalogPage, error)
}
