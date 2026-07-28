package iamRepoInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

// [COMMENT]: UserRepository định nghĩa interface cho các thao tác quản lý dữ liệu danh bạ người dùng (User Directory)
type UserRepository interface {
	// [COMMENT]: ListUsers lấy danh sách users có level thấp hơn caller level (role_level số lớn hơn)
	ListUsers(ctx context.Context, callerLevel uint8, limit int, offset int) ([]*iamEntity.User, error)
	// [COMMENT]: UpdateUserStatus cập nhật trạng thái hoạt động (status) của user dưới DB nếu đủ phân cấp
	UpdateUserStatus(ctx context.Context, callerLevel uint8, userID uuid.UUID, status string) error
	// [COMMENT]: GetUserProfile lấy thông tin profile hiển thị của user từ bảng user_profiles
	GetUserProfile(ctx context.Context, userID uuid.UUID) (*iamEntity.UserProfile, error)
	GetUserAuthMethods(ctx context.Context, callerLevel uint8, userID uuid.UUID) (*iamEntity.UserAuthMethods, error)
	// [COMMENT]: ResetUserPassword cập nhật mật khẩu của user dưới DB và lưu lịch sử mật khẩu cũ
	ResetUserPassword(ctx context.Context, callerLevel uint8, userID uuid.UUID, passwordHash string) error
}
