package iamSvcInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: RbacTenantService định nghĩa interface cho business logic RBAC trong phạm vi Tenant
type RbacTenantService interface {
	// [COMMENT]: ListTenantRoles lấy danh sách roles gán cho tenant cụ thể
	ListTenantRoles(ctx context.Context, tenantID uuid.UUID) ([]iamEntity.Role, error)

	// [COMMENT]: AssignUserRole gán role cho user thuộc tenant workspace
	AssignUserRole(ctx context.Context, userRole *iamEntity.UserRole) error

	// [COMMENT]: AssignTenantRole gán role cho tenant thuộc tenant workspace
	AssignTenantRole(ctx context.Context, tenantRole *iamEntity.TenantRole) error
}
