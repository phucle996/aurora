package mocks

import (
	"context"

	"controlplane/internal/managedservice/domain/entity"
)

type PersonalCatalogVersionService struct {
	Calls  int
	Input  *entity.GetPersonalCatalogVersion
	Result *entity.PersonalCatalogVersionView
	Err    error
}

func (s *PersonalCatalogVersionService) GetPersonalCatalogVersion(_ context.Context, in *entity.GetPersonalCatalogVersion) (*entity.PersonalCatalogVersionView, error) {
	s.Calls++
	s.Input = in
	return s.Result, s.Err
}
