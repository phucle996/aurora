package mocks

import (
	"context"

	"controlplane/internal/managedservice/domain/entity"
)

type TenantCatalogVersionService struct {
	Calls  int
	Input  *entity.GetTenantCatalogVersion
	Result *entity.TenantCatalogVersionView
	Err    error
}

func (s *TenantCatalogVersionService) GetTenantCatalogVersion(_ context.Context, in *entity.GetTenantCatalogVersion) (*entity.TenantCatalogVersionView, error) {
	s.Calls++
	s.Input = in
	return s.Result, s.Err
}
