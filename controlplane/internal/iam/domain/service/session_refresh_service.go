package iamSvcInterface

import (
	"context"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

// SessionRefreshService quản lý tất cả các hoạt động làm mới/gia hạn phiên làm việc
// của cả End-User và SRE Admin nhằm tối ưu hóa hiệu năng, giảm tải (de-bloat) cho các service khác.
type SessionRefreshService interface {
	// CreateRefreshToken tạo mới một session refresh token đục (opaque) khi đăng nhập thành công trên thiết bị tin cậy.
	CreateRefreshToken(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) (string, time.Time, error)

	// 3. Trinity Refresh cho SRE Admin — sliding session qua Redis L2 kèm CAS versioning
	RefreshAdminTrinity(ctx context.Context, zoneCode string, ip *string, userAgent *string) (iamEntity.AdminLoginResult, error)

	// Các phương thức phụ trợ thu hồi refresh token của user
	RevokeRefreshTokensByDeviceIDAndUserID(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) error
	RevokeRefreshTokensByUserID(ctx context.Context, userID uuid.UUID, exceptDeviceID *uuid.UUID) error

	// [COMMENT]: Xóa bỏ refresh token theo giá trị raw nhận được từ cookie (băm và xóa)
	RevokeOpaqueRefreshToken(ctx context.Context, rawRefreshToken string) error

	// Xác thực Opaque Refresh Token read-only từ gRPC có kèm theo context scope
	VerifyOpaqueRefreshToken(ctx context.Context, rawRefreshToken string, scope string) (*iamEntity.VerifyOpaqueRefreshTokenResult, error)
}
