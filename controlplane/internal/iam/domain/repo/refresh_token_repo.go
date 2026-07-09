package iamRepoInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

type RefreshTokenRepository interface {
	// CreateToken lưu trực tiếp một phiên làm việc refresh token mới vào database.
	CreateToken(ctx context.Context, token iamEntity.RefreshToken) error
	LoadContextByHash(ctx context.Context, tokenHash string) (*iamEntity.RefreshContext, error)
	// [COMMENT]: Xóa bỏ Refresh Token session dựa trên hash để thực hiện thu hồi/logout
	DeleteByHash(ctx context.Context, tokenHash string) (int64, error)
	DeleteByUserID(ctx context.Context, userID uuid.UUID, exceptDeviceID *uuid.UUID) (int64, error)
	DeleteByDeviceID(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) (int64, error)
	DeleteByDeviceIDs(ctx context.Context, userID uuid.UUID, deviceIDs []uuid.UUID) (int64, error)
}
