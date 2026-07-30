package service

import (
	"context"

	"controlplane/internal/managedservice/domain/entity"
	managedrepo "controlplane/internal/managedservice/domain/repo"
	managedservice "controlplane/internal/managedservice/domain/service"
)

type personalCatalogService struct {
	repo managedrepo.PersonalCatalogRepository
}

func NewPersonalCatalogService(repo managedrepo.PersonalCatalogRepository) managedservice.PersonalCatalogService {
	return &personalCatalogService{repo: repo}
}

func (s *personalCatalogService) ListPersonalCatalog(ctx context.Context, in *entity.ListPersonalCatalog) (*entity.PersonalCatalogPage, error) {
	return s.repo.ListPersonalCatalog(ctx, in)
}
