package iamSvcInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
)

type AuthService interface {
	RegisterAccount(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile, password string) error
	Login(ctx context.Context, req iamEntity.LoginRequest) (*iamEntity.LoginResult, error)
	Logout(ctx context.Context) error

	// Xác thực credentials của End-User qua gRPC
	VerifyUserTrinitySession(ctx context.Context, token string, accessKey string, accessSecret string) (*iamEntity.VerifySessionResult, error)
}
