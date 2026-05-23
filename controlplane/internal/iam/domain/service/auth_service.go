package iamSvcInterface

import (
	"context"

	"controlplane/internal/iam/domain/entity"
)

type AuthService interface {
	RegisterAccount(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile, password string) error
	Login(ctx context.Context, req iamEntity.LoginRequest) (*iamEntity.LoginResult, error)
	Logout(ctx context.Context, userID string, trackingID string) error
}
