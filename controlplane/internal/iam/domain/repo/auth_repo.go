package iamRepoInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
)

type AuthRepository interface {
	GetLoginUserByUsername(ctx context.Context, username string) (*iamEntity.LoginUser, error)
	CreateRegisteredUser(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile) error
}
