package tenantRepo

import (
	"context"

	tenantEntity "controlplane/internal/tenant/domain/entity"
)

type Repository interface {
	CreateTenantBootstrapTx(ctx context.Context, input tenantEntity.CreateTenantInput) (*tenantEntity.CreateTenantResult, error)
	ResolveTenantByDomain(ctx context.Context, domain string) (*tenantEntity.TenantDomain, error)
	GetMembershipAndRoles(ctx context.Context, tenantID string, userID string) (*tenantEntity.LoginTenantContext, error)
}
