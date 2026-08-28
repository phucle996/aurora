package iamSvcInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
)

// [COMMENT]: TenantRbacService định nghĩa interface cho business logic RBAC trong phạm vi Tenant
type TenantRbacService interface {
	ListTenantRoles(context.Context, *iamEntity.ListTenantRoles) ([]iamEntity.ListTenantRoles, error)
	CreateTenantRole(context.Context, *iamEntity.CreateTenantRole) (*iamEntity.CreateTenantRole, error)
	GetTenantRole(context.Context, *iamEntity.GetTenantRole) (*iamEntity.GetTenantRole, error)
	CreateTenantRoleRevision(context.Context, *iamEntity.CreateTenantRoleRevision) (*iamEntity.CreateTenantRoleRevision, error)
	UpgradeTenantRoleAssignments(context.Context, *iamEntity.UpgradeTenantRoleAssignments) (*iamEntity.UpgradeTenantRoleAssignments, error)
	ResolveTenantAccess(context.Context, *iamEntity.ResolveTenantAccess) (*iamEntity.ResolveTenantAccess, error)
}
