package iamSvcInterface

import (
	"context"

	"controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

type AuthService interface {
	RegisterAccount(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile, password string) error
	Login(ctx context.Context, req iamEntity.LoginRequest) (*iamEntity.LoginResult, error)
	Logout(ctx context.Context, userID uuid.UUID, accessKey string, accessSecret string) error
	
	// Xác thực credentials của Admin/SRE quản trị qua gRPC
	VerifyAdminTrinitySession(ctx context.Context, token string, accessKey string, accessSecret string) (*iamEntity.VerifySessionResult, error)
	
	// Xác thực credentials của End-User qua gRPC
	VerifyUserTrinitySession(ctx context.Context, token string, accessKey string, accessSecret string) (*iamEntity.VerifySessionResult, error)
}
