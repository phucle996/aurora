package iamSvcInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: RbacService định nghĩa interface tối giản (skeleton) cho các business logic liên quan đến RBAC
type RbacService interface {
	// [COMMENT]: GetUserRolePermissions lấy danh sách permissions dạng binary (bytea) của user trên toàn bộ workspaces theo user id
	GetUserRolePermissions(ctx context.Context, userID uuid.UUID) ([]byte, error)

	// [COMMENT]: AssignUserRole gán role và permissions tĩnh cho user trong một workspace
	AssignUserRole(ctx context.Context, userRole *iamEntity.UserRole) error

	// [COMMENT]: AssignTenantRole gán role và permissions tĩnh cho tenant trong một workspace
	AssignTenantRole(ctx context.Context, tenantRole *iamEntity.TenantRole) error

	// [COMMENT]: GetUserRoleDetails lấy thông tin chi tiết vai trò của user kèm kiểm tra cấp bậc
	GetUserRoleDetails(ctx context.Context, userID uuid.UUID, callerLevel int32) (*iamEntity.Role, error)

	// [COMMENT]: ListPlatformRoles lấy toàn bộ danh sách roles có scope là platform
	ListPlatformRoles(ctx context.Context) ([]iamEntity.Role, error)

	// [COMMENT]: ListTenantRoles lấy danh sách roles gán cho tenant cụ thể
	ListTenantRoles(ctx context.Context, tenantID uuid.UUID) ([]iamEntity.Role, error)

	// [COMMENT]: GetRenderContext sinh cấu hình Navigation và Capabilities từ bytes RBAC L1 cache theo user id
	GetRenderContext(ctx context.Context, userID uuid.UUID) (*iamEntity.RenderContext, error)

	// [COMMENT]: CreateRole tạo một vai trò mới kèm theo gán permissions
	CreateRole(ctx context.Context, role *iamEntity.Role, permissionIDs []uuid.UUID) error

	// [COMMENT]: ListPermissions lấy toàn bộ danh sách permissions có trong hệ thống
	ListPermissions(ctx context.Context) ([]iamEntity.Permission, error)
}
