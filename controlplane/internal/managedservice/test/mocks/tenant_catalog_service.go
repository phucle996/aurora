package mocks

import (
	"context"

	"controlplane/internal/managedservice/domain/entity"
)

type TenantCatalogService struct {
	Calls  int
	Input  *entity.ListTenantCatalog
	Result *entity.TenantCatalogPage
	Err    error
}

func (s *TenantCatalogService) ListTenantCatalog(_ context.Context, in *entity.ListTenantCatalog) (*entity.TenantCatalogPage, error) {
	s.Calls++
	s.Input = in
	return s.Result, s.Err
}
