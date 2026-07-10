package iamSvcInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: UserService định nghĩa các business logic liên quan đến quản trị người dùng (User Directory Management)
type UserService interface {
	// [COMMENT]: ListUsers lấy danh sách users thô từ repository có role_level lớn hơn caller level (quyền lực nhỏ hơn)
	ListUsers(ctx context.Context, callerLevel uint8, limit int, offset int) ([]*iamEntity.User, error)
	// [COMMENT]: UpdateUserStatus thực hiện vô hiệu hóa (disable) hoặc cập nhật trạng thái hoạt động của user và thu hồi cache
	UpdateUserStatus(ctx context.Context, callerLevel uint8, targetUserID uuid.UUID, status string) error
	// [COMMENT]: GetUserProfile trả về thông tin profile hiển thị của user (fullname, avatar, v.v.)
	GetUserProfile(ctx context.Context, userID uuid.UUID) (*iamEntity.UserProfile, error)
	// [COMMENT]: ResetUserPassword thực hiện thay đổi mật khẩu của user bởi Admin phân cấp
	ResetUserPassword(ctx context.Context, callerLevel uint8, targetUserID uuid.UUID, newPassword string) error
}
