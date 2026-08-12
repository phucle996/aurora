package iamSvcInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: RbacPlatformService định nghĩa interface cho business logic RBAC cấp độ platform (toàn cục)
type RbacPlatformService interface {
	// [COMMENT]: AssignUserRole gán vai trò hệ thống cho user có kiểm tra phân cấp
	AssignUserRole(ctx context.Context, callerLevel uint8, userID uuid.UUID, roleID uuid.UUID) error

	// [COMMENT]: GetUserRoleDetails lấy thông tin chi tiết vai trò của user kèm kiểm tra cấp bậc
	GetUserRoleDetails(ctx context.Context, userID uuid.UUID, callerLevel int32) (*iamEntity.Role, error)

	// [COMMENT]: ListPlatformRoles lấy danh sách roles có scope là platform có level thấp hơn (role_level > callerLevel)
	ListPlatformRoles(ctx context.Context, callerLevel uint8) ([]iamEntity.Role, error)

	// [COMMENT]: CreateRole tạo vai trò hệ thống mới kèm liên kết permissions và kiểm tra giới hạn tập con quyền của caller
	CreateRole(ctx context.Context, callerUserID uuid.UUID, role *iamEntity.Role, permissionIDs []uuid.UUID) error

	// [COMMENT]: ListPermissions lấy danh sách permissions catalog được lọc dựa theo quyền của caller
	ListPermissions(ctx context.Context, callerUserID uuid.UUID) ([]iamEntity.Permission, error)

	// [COMMENT]: GetUserRolePermissions lấy danh sách permissions binary của user theo user id
	GetUserRolePermissions(ctx context.Context, userID uuid.UUID) ([]byte, error)
	ResolvePersonalRoleLevel(ctx context.Context, userID uuid.UUID) (int32, error)

	// [COMMENT]: DeleteRolePlatform xóa vai trò platform nếu callerLevel < roleLevel và không còn user/tenant nào được gán
	DeleteRolePlatform(ctx context.Context, callerLevel uint8, roleID uuid.UUID) error

	// [COMMENT]: GetRoleDetails lấy chi tiết một vai trò platform cùng danh sách đối tượng permission bậc 3
	GetRoleDetails(ctx context.Context, callerLevel uint8, roleID uuid.UUID) (*iamEntity.Role, []iamEntity.Permission, error)

	// [COMMENT]: UpdateRole cập nhật thông tin vai trò platform cùng danh sách permissions được gán có kiểm tra cấp bậc caller level
	UpdateRole(ctx context.Context, callerUserID uuid.UUID, callerLevel uint8, input *iamEntity.UpdateRoleInput) error
}
