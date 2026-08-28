package iamRepoInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: TenantRbacRepository định nghĩa interface quản lý RBAC trong phạm vi Tenant (phân lập doanh nghiệp)
type TenantRbacRepository interface {
	ListTenantRoles(context.Context, *iamEntity.ListTenantRoles) ([]iamEntity.ListTenantRoles, error)
	CreateTenantRole(context.Context, *iamEntity.CreateTenantRole) (*iamEntity.CreateTenantRole, error)
	GetTenantRole(context.Context, *iamEntity.GetTenantRole) (*iamEntity.GetTenantRole, error)
	CreateTenantRoleRevision(context.Context, *iamEntity.CreateTenantRoleRevision) (*iamEntity.CreateTenantRoleRevision, error)
	UpgradeTenantRoleAssignments(context.Context, *iamEntity.UpgradeTenantRoleAssignments) (*iamEntity.UpgradeTenantRoleAssignments, error)
	ResolveTenantAccess(context.Context, *iamEntity.ResolveTenantAccess) (*iamEntity.ResolveTenantAccess, error)

	// GetUserTenantBillingPermissions resolves the active membership role for
	// one user+tenant tuple and returns canonical five-part keys.
	GetUserTenantBillingPermissions(ctx context.Context, userID uuid.UUID, tenantID uuid.UUID) ([]byte, error)
	GetUserTenantRolePermissions(ctx context.Context, userID uuid.UUID, tenantID uuid.UUID) ([]byte, error)
}
