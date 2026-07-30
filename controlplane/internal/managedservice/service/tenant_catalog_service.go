package service

import (
	"context"

	"controlplane/internal/managedservice/domain/entity"
	managedrepo "controlplane/internal/managedservice/domain/repo"
	managedservice "controlplane/internal/managedservice/domain/service"
)

type tenantCatalogService struct {
	repo managedrepo.TenantCatalogRepository
}

func NewTenantCatalogService(repo managedrepo.TenantCatalogRepository) managedservice.TenantCatalogService {
	return &tenantCatalogService{repo: repo}
}

func (s *tenantCatalogService) ListTenantCatalog(ctx context.Context, in *entity.ListTenantCatalog) (*entity.TenantCatalogPage, error) {
	return s.repo.ListTenantCatalog(ctx, in)
}
