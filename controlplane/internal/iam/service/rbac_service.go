package iamSvcImpl

import (
	"context"

	"controlplane/internal/cacheengine"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"

	"github.com/google/uuid"
)

// [COMMENT]: RbacService thực hiện interface RbacService tối giản dạng skeleton cho phase tiếp theo
type RbacService struct {
	repo        iamRepoInterface.RbacRepository
	cacheEngine *cacheengine.CacheRegistry
}

// [COMMENT]: NewRbacService khởi tạo một thể hiện mới của RbacService
func NewRbacService(
	repo iamRepoInterface.RbacRepository,
	cacheEngine *cacheengine.CacheRegistry,
) iamSvcInterface.RbacService {
	return &RbacService{
		repo:        repo,
		cacheEngine: cacheEngine,
	}
}

// [COMMENT]: GetUserRolePermissions lấy danh sách permissions binary của user trong workspace (skeleton)
func (s *RbacService) GetUserRolePermissions(ctx context.Context, userID uuid.UUID, workspaceID uuid.UUID) ([]byte, error) {
	// [COMMENT]: Logic nghiệp vụ và kiểm tra cache sẽ được viết ở phase tiếp theo
	return nil, nil
}

// [COMMENT]: AssignUserRole gán role và permissions cho user (skeleton)
func (s *RbacService) AssignUserRole(ctx context.Context, userRole *iamEntity.UserRole) error {
	// [COMMENT]: Logic nghiệp vụ gán role và build binary list_perm sẽ được viết ở phase tiếp theo
	return nil
}

// [COMMENT]: AssignTenantRole gán role và permissions cho tenant (skeleton)
func (s *RbacService) AssignTenantRole(ctx context.Context, tenantRole *iamEntity.TenantRole) error {
	// [COMMENT]: Logic nghiệp vụ gán role và build binary list_perm sẽ được viết ở phase tiếp theo
	return nil
}
