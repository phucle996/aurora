package iamRepoInterface

import (
	"context"

	"github.com/google/uuid"
)

// MfaRepository định nghĩa interface kiểm tra thông tin MFA của người dùng phục vụ platform audit.
type MfaRepository interface {
	// GetUserMfaStatus lấy trạng thái MFA của một user ID.
	GetUserMfaStatus(ctx context.Context, userID uuid.UUID) (bool, string, error)
}
