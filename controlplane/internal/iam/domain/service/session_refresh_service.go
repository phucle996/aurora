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
	IssueDeviceRefreshToken(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) (string, time.Time, error)

	RecoverUserSession(ctx context.Context, rawRefreshToken string, requestedTenantID *uuid.UUID) (*iamEntity.RecoverUserSession, error)

	RevokeOpaqueRefreshToken(ctx context.Context, rawRefreshToken string) error
}
