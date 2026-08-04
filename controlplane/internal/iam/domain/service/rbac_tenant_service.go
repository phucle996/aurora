package iamSvcInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
)

// [COMMENT]: RbacTenantService định nghĩa interface cho business logic RBAC trong phạm vi Tenant
type RbacTenantService interface {
	ListTenantRoles(context.Context, *iamEntity.ListTenantRoles) ([]iamEntity.ListTenantRoles, error)
	CreateTenantRole(context.Context, *iamEntity.CreateTenantRole) (*iamEntity.CreateTenantRole, error)
	ResolveTenantAccess(context.Context, *iamEntity.ResolveTenantAccess) (*iamEntity.ResolveTenantAccess, error)
}
