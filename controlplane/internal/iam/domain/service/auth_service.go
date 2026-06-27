package iamSvcInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
)

type AuthService interface {
	RegisterAccount(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile, password string) error

	// Xác thực thông tin đăng nhập và thông tin thiết bị thô của người dùng qua gRPC
	VerifyUserCredentials(ctx context.Context, req iamEntity.LoginRequest) (*iamEntity.VerifyUserCredentialsResult, error)

	// Stop dọn dẹp tài nguyên và đợi các tác vụ bất đồng bộ nền hoàn tất (Graceful Shutdown)
	Stop()
}
