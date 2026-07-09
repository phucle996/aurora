package iamSvcInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: RbacPlatformService định nghĩa interface cho business logic RBAC cấp độ platform (toàn cục)
type RbacPlatformService interface {
	// [COMMENT]: AssignUserRole gán vai trò hệ thống cho user
	AssignUserRole(ctx context.Context, userRole *iamEntity.UserRole) error

	// [COMMENT]: AssignTenantRole gán vai trò hệ thống cho tenant
	AssignTenantRole(ctx context.Context, tenantRole *iamEntity.TenantRole) error

	// [COMMENT]: GetUserRoleDetails lấy thông tin chi tiết vai trò của user kèm kiểm tra cấp bậc
	GetUserRoleDetails(ctx context.Context, userID uuid.UUID, callerLevel int32) (*iamEntity.Role, error)

	// [COMMENT]: ListPlatformRoles lấy toàn bộ danh sách roles có scope là platform
	ListPlatformRoles(ctx context.Context) ([]iamEntity.Role, error)

	// [COMMENT]: CreateRole tạo vai trò hệ thống mới kèm liên kết permissions
	CreateRole(ctx context.Context, role *iamEntity.Role, permissionIDs []uuid.UUID) error

	// [COMMENT]: ListPermissions lấy toàn bộ danh sách permissions catalog trong hệ thống
	ListPermissions(ctx context.Context) ([]iamEntity.Permission, error)

	// [COMMENT]: GetUserRolePermissions lấy danh sách permissions binary của user theo user id
	GetUserRolePermissions(ctx context.Context, userID uuid.UUID) ([]byte, error)

	// [COMMENT]: GetRenderContext sinh cấu hình Navigation và Capabilities từ bytes RBAC L1 cache theo user id
	GetRenderContext(ctx context.Context, userID uuid.UUID) (*iamEntity.RenderContext, error)
}
