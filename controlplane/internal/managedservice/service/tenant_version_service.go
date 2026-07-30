package service

import (
	"context"

	"controlplane/internal/managedservice/domain/entity"
	managedrepo "controlplane/internal/managedservice/domain/repo"
	managedservice "controlplane/internal/managedservice/domain/service"
)

type tenantCatalogVersionService struct {
	repo managedrepo.TenantCatalogVersionRepository
}

func NewTenantCatalogVersionService(repo managedrepo.TenantCatalogVersionRepository) managedservice.TenantCatalogVersionService {
	return &tenantCatalogVersionService{repo: repo}
}

func (s *tenantCatalogVersionService) GetTenantCatalogVersion(ctx context.Context, in *entity.GetTenantCatalogVersion) (*entity.TenantCatalogVersionView, error) {
	return s.repo.GetTenantCatalogVersion(ctx, in)
}
