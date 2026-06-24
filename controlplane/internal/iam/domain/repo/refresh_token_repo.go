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
	// [COMMENT]: Xóa bỏ Refresh Token session dựa trên hash để thực hiện thu hồi/logout
	DeleteRefreshTokenSessionByHash(ctx context.Context, tokenHash string) (int64, error)
	RevokeRefreshTokensByUserID(ctx context.Context, userID uuid.UUID, exceptDeviceID *uuid.UUID) (int64, error)
	RevokeRefreshTokensByDeviceIDAndUserID(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) (int64, error)
	RevokeRefreshTokensByDeviceIDsAndUserID(ctx context.Context, userID uuid.UUID, deviceIDs []uuid.UUID) (int64, error)
}
