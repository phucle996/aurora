package iamSvcInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: RbacService định nghĩa interface tối giản (skeleton) cho các business logic liên quan đến RBAC
type RbacService interface {
	// [COMMENT]: GetUserRolePermissions lấy danh sách permissions dạng binary (bytea) của user trong workspace
	GetUserRolePermissions(ctx context.Context, userID uuid.UUID, workspaceID uuid.UUID) ([]byte, error)

	// [COMMENT]: AssignUserRole gán role và permissions tĩnh cho user trong một workspace
	AssignUserRole(ctx context.Context, userRole *iamEntity.UserRole) error

	// [COMMENT]: AssignTenantRole gán role và permissions tĩnh cho tenant trong một workspace
	AssignTenantRole(ctx context.Context, tenantRole *iamEntity.TenantRole) error

	// [COMMENT]: ListPlatformRoles lấy toàn bộ danh sách roles có scope là platform
	ListPlatformRoles(ctx context.Context) ([]iamEntity.Role, error)
}
