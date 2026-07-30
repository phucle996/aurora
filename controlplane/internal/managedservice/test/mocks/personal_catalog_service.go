package mocks

import (
	"context"

	"controlplane/internal/managedservice/domain/entity"
)

type PersonalCatalogService struct {
	Calls  int
	Input  *entity.ListPersonalCatalog
	Result *entity.PersonalCatalogPage
	Err    error
}

func (s *PersonalCatalogService) ListPersonalCatalog(_ context.Context, in *entity.ListPersonalCatalog) (*entity.PersonalCatalogPage, error) {
	s.Calls++
	s.Input = in
	return s.Result, s.Err
}
