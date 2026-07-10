package iamRepoInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: RbacPlatformRepository định nghĩa interface quản lý RBAC ở cấp độ platform (quản trị toàn cục)
type RbacPlatformRepository interface {
	// [COMMENT]: AssignUserRole gán role và permissions cho user hệ thống có kiểm tra phân cấp
	AssignUserRole(ctx context.Context, callerLevel uint8, userID uuid.UUID, roleID uuid.UUID) error

	// [COMMENT]: GetUserRoleDetails lấy thông tin chi tiết vai trò của user kèm kiểm tra cấp bậc
	GetUserRoleDetails(ctx context.Context, userID uuid.UUID, callerLevel int32) (*iamEntity.Role, error)

	// [COMMENT]: ListPlatformRoles lấy toàn bộ danh sách roles có scope là platform có level thấp hơn (role_level > callerLevel)
	ListPlatformRoles(ctx context.Context, callerLevel uint8) ([]iamEntity.Role, error)

	// [COMMENT]: CreateRole tạo một vai trò hệ thống mới và map danh sách permissions
	CreateRole(ctx context.Context, role *iamEntity.Role, permissionIDs []uuid.UUID) error

	// [COMMENT]: ListPermissions lấy danh sách toàn bộ permissions catalog trong hệ thống
	ListPermissions(ctx context.Context) ([]iamEntity.Permission, error)

	// [COMMENT]: GetUserRolePermissions lấy danh sách permissions binary của user theo user id
	GetUserRolePermissions(ctx context.Context, userID uuid.UUID) ([]byte, error)

	// [COMMENT]: GetRoleIDByUserID lấy role_id và level của user tại platform scope (nil UUID) phục vụ check session
	GetRoleIDByUserID(ctx context.Context, userID uuid.UUID) (string, int32, error)
}
