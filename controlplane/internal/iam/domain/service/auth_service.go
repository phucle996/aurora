package iamSvcInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

type AuthService interface {
	RegisterAccount(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile, password string) error

	// [COMMENT]: VerifyAccount thực hiện kiểm tra token kích hoạt tài khoản
	// và tiến hành active trạng thái của user kèm theo gán role mặc định.
	VerifyAccount(ctx context.Context, userID uuid.UUID, token string) error

	// Xác thực thông tin đăng nhập và thông tin thiết bị thô của người dùng qua gRPC
	VerifyUserCredentials(ctx context.Context, req iamEntity.LoginRequest) (*iamEntity.VerifyUserCredentialsResult, error)

	// Stop dọn dẹp tài nguyên và đợi các tác vụ bất đồng bộ nền hoàn tất (Graceful Shutdown)
	Stop()
}
