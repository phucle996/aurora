package iamSvcInterface

import (
	"context"

	"github.com/google/uuid"
)

// MfaService định nghĩa interface quản lý và kiểm tra trạng thái MFA cấp hệ thống phục vụ platform audit.
type MfaService interface {
	// GetUserMfaStatus lấy trạng thái MFA chi tiết của một user ID.
	GetUserMfaStatus(ctx context.Context, userID uuid.UUID) (bool, string, error)
}
