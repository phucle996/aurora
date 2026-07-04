package iamRepoInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: RbacRepository định nghĩa interface tối giản (skeleton) cho các thao tác dữ liệu liên quan đến user_role và tenant_role
type RbacRepository interface {
	// [COMMENT]: GetUserRolePermissions lấy danh sách permissions dạng binary (bytea) của user trong workspace
	GetUserRolePermissions(ctx context.Context, userID uuid.UUID, workspaceID uuid.UUID) ([]byte, error)

	// [COMMENT]: GetTenantRolePermissions lấy danh sách permissions dạng binary (bytea) của tenant trong workspace
	GetTenantRolePermissions(ctx context.Context, tenantID uuid.UUID, workspaceID uuid.UUID, roleID uuid.UUID) ([]byte, error)

	// [COMMENT]: AssignUserRole gán role và permissions tĩnh cho user trong một workspace
	AssignUserRole(ctx context.Context, userRole *iamEntity.UserRole) error

	// [COMMENT]: AssignTenantRole gán role và permissions tĩnh cho tenant trong một workspace
	AssignTenantRole(ctx context.Context, tenantRole *iamEntity.TenantRole) error

	// [COMMENT]: GetRoleIDByUserID lấy role_id và level của user tại platform scope (nil UUID)
	GetRoleIDByUserID(ctx context.Context, userID uuid.UUID) (string, int32, error)

	// [COMMENT]: GetRoleIDByTenantID lấy role_id và level của tenant tại platform scope (nil UUID)
	GetRoleIDByTenantID(ctx context.Context, tenantID uuid.UUID) (string, int32, error)

	// [COMMENT]: ListPlatformRoles lấy toàn bộ danh sách roles có scope là platform
	ListPlatformRoles(ctx context.Context) ([]iamEntity.Role, error)

	// [COMMENT]: ListTenantRoles lấy danh sách roles gán cho tenant cụ thể
	ListTenantRoles(ctx context.Context, tenantID uuid.UUID) ([]iamEntity.Role, error)
}
