package service

import (
	"context"

	"controlplane/internal/managedservice/domain/entity"
)

type TenantCatalogService interface {
	ListTenantCatalog(context.Context, *entity.ListTenantCatalog) (*entity.TenantCatalogPage, error)
}
