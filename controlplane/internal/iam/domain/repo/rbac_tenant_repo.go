package iamRepoInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: RbacTenantRepository định nghĩa interface quản lý RBAC trong phạm vi Tenant (phân lập doanh nghiệp)
type RbacTenantRepository interface {
	// [COMMENT]: ListTenantRoles lấy danh sách roles được gán/sử dụng bởi tenant cụ thể
	ListTenantRoles(ctx context.Context, tenantID uuid.UUID) ([]iamEntity.Role, error)

	// [COMMENT]: AssignUserRole gán role cho user thuộc tenant workspace
	AssignUserRole(ctx context.Context, userRole *iamEntity.UserRole) error

	// [COMMENT]: AssignTenantRole gán role cho tenant thuộc tenant workspace
	AssignTenantRole(ctx context.Context, tenantRole *iamEntity.TenantRole) error

	// [COMMENT]: GetTenantRolePermissions lấy danh sách permissions binary của tenant theo role
	GetTenantRolePermissions(ctx context.Context, tenantID uuid.UUID, roleID uuid.UUID) ([]byte, error)

	// [COMMENT]: GetRoleIDByTenantID lấy role_id và level của tenant tại platform scope (nil UUID) phục vụ check session
	GetRoleIDByTenantID(ctx context.Context, tenantID uuid.UUID) (string, int32, error)
}
