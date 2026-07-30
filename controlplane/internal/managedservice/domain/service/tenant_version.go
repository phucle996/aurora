package service

import (
	"context"

	"controlplane/internal/managedservice/domain/entity"
)

type TenantCatalogVersionService interface {
	GetTenantCatalogVersion(context.Context, *entity.GetTenantCatalogVersion) (*entity.TenantCatalogVersionView, error)
}
