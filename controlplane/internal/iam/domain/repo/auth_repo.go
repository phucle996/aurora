package iamRepoInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
)

type AuthRepository interface {
	CheckUserExist(ctx context.Context, username string, email string) (bool, error)
	GetLoginUserByUsername(ctx context.Context, username string) (*iamEntity.LoginUser, error)
	CreateRegisteredUser(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile) error
	CreateRefreshTokenSession(ctx context.Context, token iamEntity.RefreshToken) error
}
