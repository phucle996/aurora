package tenantSvc

import (
	"context"

	tenantEntity "controlplane/internal/tenant/domain/entity"
)

type Service interface {
	CreateTenant(ctx context.Context, input tenantEntity.CreateTenantInput) (*tenantEntity.CreateTenantResult, error)
	ResolveLoginContextByDomain(ctx context.Context, domain string, userID string) (*tenantEntity.LoginTenantContext, error)
}
