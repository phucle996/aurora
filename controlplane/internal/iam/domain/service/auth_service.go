package iamSvcInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
)

type AuthService interface {
	RegisterAccount(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile, password string) error

	// Xác thực thông tin đăng nhập và thông tin thiết bị thô của người dùng qua gRPC
	VerifyUserCredentials(ctx context.Context, req iamEntity.LoginRequest) (*iamEntity.VerifyUserCredentialsResult, error)

	// Xác thực Opaque Refresh Token (gọi nội bộ từ gRPC)
	VerifyOpaqueRefreshToken(ctx context.Context, refreshToken string, scope string) (*iamEntity.VerifyOpaqueRefreshTokenResult, error)
}
