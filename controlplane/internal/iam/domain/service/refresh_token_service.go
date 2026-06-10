package iamSvcInterface

import (
	"context"

	"controlplane/internal/iam/domain/entity"
	"github.com/google/uuid"
)

type RefreshTokenService interface {
	Refresh(ctx context.Context, rawRefreshToken string) (*iamEntity.RefreshTokenResult, error)
	RevokeRefreshTokensByDeviceIDAndUserID(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) error
	RevokeRefreshTokensByUserID(ctx context.Context, userID uuid.UUID, exceptDeviceID *uuid.UUID) error
}
