package iamSvcInterface

import (
	"context"

	"controlplane/internal/iam/domain/entity"
)

type RefreshTokenService interface {
	Refresh(ctx context.Context, rawRefreshToken string) (*iamEntity.RefreshTokenResult, error)
}
