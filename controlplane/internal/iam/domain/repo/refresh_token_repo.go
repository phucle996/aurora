package iamRepoInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
)

type RefreshTokenRepository interface {
	IssueDeviceRefreshToken(context.Context, *iamEntity.IssueDeviceRefreshToken) error
	RecoverUserSession(context.Context, *iamEntity.RecoverUserSession) (*iamEntity.RecoverUserSession, error)
	DeleteByHash(context.Context, string) (int64, error)
}
