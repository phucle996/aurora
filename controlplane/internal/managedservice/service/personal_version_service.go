package service

import (
	"context"

	"controlplane/internal/managedservice/domain/entity"
	managedrepo "controlplane/internal/managedservice/domain/repo"
	managedservice "controlplane/internal/managedservice/domain/service"
)

type personalCatalogVersionService struct {
	repo managedrepo.PersonalCatalogVersionRepository
}

func NewPersonalCatalogVersionService(repo managedrepo.PersonalCatalogVersionRepository) managedservice.PersonalCatalogVersionService {
	return &personalCatalogVersionService{repo: repo}
}

func (s *personalCatalogVersionService) GetPersonalCatalogVersion(ctx context.Context, in *entity.GetPersonalCatalogVersion) (*entity.PersonalCatalogVersionView, error) {
	return s.repo.GetPersonalCatalogVersion(ctx, in)
}
