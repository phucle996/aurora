package iamRepoInterface

import (
	"context"

	"controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

type RefreshTokenRepository interface {
	// CreateRefreshTokenSession lưu trực tiếp một phiên làm việc refresh token mới vào database.
	CreateRefreshTokenSession(ctx context.Context, token iamEntity.RefreshToken) error
	LoadRefreshContextByHash(ctx context.Context, tokenHash string) (*iamEntity.RefreshContext, error)
	GetRefreshTokenByHash(ctx context.Context, tokenHash string) (*iamEntity.RefreshTokenSession, error)
	GetRefreshTokenUserByID(ctx context.Context, userID uuid.UUID) (*iamEntity.RefreshTokenUser, error)
	GetRefreshTokenDeviceByID(ctx context.Context, deviceID uuid.UUID) (*iamEntity.RefreshTokenDevice, error)
	RevokeRefreshTokensByUserID(ctx context.Context, userID uuid.UUID, exceptDeviceID *uuid.UUID) (int64, error)
	RevokeRefreshTokensByDeviceIDAndUserID(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) (int64, error)
	RevokeRefreshTokensByDeviceIDsAndUserID(ctx context.Context, userID uuid.UUID, deviceIDs []uuid.UUID) (int64, error)
	RotateRefreshToken(ctx context.Context, current iamEntity.RefreshTokenSession, next iamEntity.RefreshToken) error
}
