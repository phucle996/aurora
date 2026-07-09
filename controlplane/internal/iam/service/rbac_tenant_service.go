package iamSvcImpl

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"

	"github.com/google/uuid"
)

// [COMMENT]: RbacTenantService thực thi interface quản lý vai trò trong phạm vi tenant
type RbacTenantService struct {
	repo iamRepoInterface.RbacTenantRepository
}

// [COMMENT]: NewRbacTenantService khởi tạo một thể hiện mới của RbacTenantService
func NewRbacTenantService(repo iamRepoInterface.RbacTenantRepository) iamSvcInterface.RbacTenantService {
	return &RbacTenantService{
		repo: repo,
	}
}

// [COMMENT]: ListTenantRoles lấy danh sách vai trò của một tenant
func (s *RbacTenantService) ListTenantRoles(ctx context.Context, tenantID uuid.UUID) ([]iamEntity.Role, error) {
	return s.repo.ListTenantRoles(ctx, tenantID)
}

// [COMMENT]: AssignUserRole gán vai trò trong phạm vi tenant cho user (skeleton)
func (s *RbacTenantService) AssignUserRole(ctx context.Context, userRole *iamEntity.UserRole) error {
	// [COMMENT]: Sẽ hiện thực hóa ở phase tiếp theo
	return nil
}

// [COMMENT]: AssignTenantRole gán vai trò trong phạm vi tenant cho tenant con (skeleton)
func (s *RbacTenantService) AssignTenantRole(ctx context.Context, tenantRole *iamEntity.TenantRole) error {
	// [COMMENT]: Sẽ hiện thực hóa ở phase tiếp theo
	return nil
}
