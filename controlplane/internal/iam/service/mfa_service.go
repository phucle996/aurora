package iamSvcImpl

import (
	"context"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"

	"github.com/google/uuid"
)

type MfaService struct {
	repo iamRepoInterface.MfaRepository
}

// NewMfaService khởi tạo service quản lý nghiệp vụ MFA
func NewMfaService(repo iamRepoInterface.MfaRepository) iamSvcInterface.MfaService {
	return &MfaService{
		repo: repo,
	}
}

// GetUserMfaStatus lấy trạng thái cấu hình MFA của người dùng
func (s *MfaService) GetUserMfaStatus(ctx context.Context, userID uuid.UUID) (bool, string, error) {
	// [COMMENT]: Chuyển tiếp yêu cầu truy vấn đến repository
	return s.repo.GetUserMfaStatus(ctx, userID)
}
