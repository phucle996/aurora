package iamSvcImpl

import (
	"context"
	"fmt"

	"controlplane/internal/cacheengine"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamproto "controlplane/internal/iam/transport/rpc/proto"

	"strings"

	"github.com/google/uuid"
)

// [COMMENT]: RbacPlatformService thực thi interface quản lý vai trò và phân quyền cấp hệ thống (platform)
type RbacPlatformService struct {
	repo        iamRepoInterface.RbacPlatformRepository
	cacheEngine *cacheengine.CacheRegistry
}

// [COMMENT]: NewRbacPlatformService khởi tạo một thể hiện mới của RbacPlatformService
func NewRbacPlatformService(
	repo iamRepoInterface.RbacPlatformRepository,
	cacheEngine *cacheengine.CacheRegistry,
) iamSvcInterface.RbacPlatformService {
	return &RbacPlatformService{
		repo:        repo,
		cacheEngine: cacheEngine,
	}
}

// [COMMENT]: AssignUserRole gán vai trò platform cho user (skeleton)
func (s *RbacPlatformService) AssignUserRole(ctx context.Context, userRole *iamEntity.UserRole) error {
	// [COMMENT]: Sẽ hiện thực hóa ở phase tiếp theo
	return nil
}

// [COMMENT]: AssignTenantRole gán vai trò platform cho tenant (skeleton)
func (s *RbacPlatformService) AssignTenantRole(ctx context.Context, tenantRole *iamEntity.TenantRole) error {
	// [COMMENT]: Sẽ hiện thực hóa ở phase tiếp theo
	return nil
}

// [COMMENT]: ListPlatformRoles trả về danh sách vai trò hệ thống
func (s *RbacPlatformService) ListPlatformRoles(ctx context.Context) ([]iamEntity.Role, error) {
	return s.repo.ListPlatformRoles(ctx)
}

// [COMMENT]: CreateRole tạo vai trò hệ thống mới và map permissions
func (s *RbacPlatformService) CreateRole(ctx context.Context, role *iamEntity.Role, permissionIDs []uuid.UUID) error {
	role.ID = uuid.New()
	return s.repo.CreateRole(ctx, role, permissionIDs)
}

// [COMMENT]: ListPermissions lấy danh sách permissions catalog hệ thống
func (s *RbacPlatformService) ListPermissions(ctx context.Context) ([]iamEntity.Permission, error) {
	return s.repo.ListPermissions(ctx)
}

// [COMMENT]: GetUserRoleDetails lấy thông tin chi tiết vai trò của user hệ thống
func (s *RbacPlatformService) GetUserRoleDetails(ctx context.Context, userID uuid.UUID, callerLevel int32) (*iamEntity.Role, error) {
	return s.repo.GetUserRoleDetails(ctx, userID, callerLevel)
}

// [COMMENT]: GetUserRolePermissions lấy danh sách permissions binary của user theo user id
func (s *RbacPlatformService) GetUserRolePermissions(ctx context.Context, userID uuid.UUID) ([]byte, error) {
	return s.repo.GetUserRolePermissions(ctx, userID)
}

// [COMMENT]: GetRenderContext sinh cấu hình Navigation và Capabilities từ bytes/object RBAC L1 cache theo user id
func (s *RbacPlatformService) GetRenderContext(ctx context.Context, userID uuid.UUID) (*iamEntity.RenderContext, error) {
	// [COMMENT]: 1. Truy vấn object RoleEntry từ CacheRegistry L1/L2
	val, err := s.cacheEngine.GetOrLoad(ctx, "user_role", userID.String())
	if err != nil {
		return nil, fmt.Errorf("rbac platform service: get user render permissions: %w", err)
	}

	entry, ok := val.(*iamproto.RoleEntry)
	if !ok || entry == nil {
		return &iamEntity.RenderContext{
			Navigation:   []iamEntity.NavigationItem{},
			Capabilities: map[string]bool{},
		}, nil
	}

	// [COMMENT]: 2. Trích xuất danh sách permissions thô (permissions string)
	rawPerms := entry.Permissions

	capabilities := make(map[string]bool)
	groupMap := make(map[string][]string)
	for _, p := range rawPerms {
		capabilities[p] = true

		parts := strings.Split(p, ":")
		if len(parts) < 3 {
			continue
		}
		// logic: module:object:behavior -> key = module.object, action = behavior
		key := parts[0] + "." + parts[1]
		behavior := parts[2]

		// Đảm bảo không trùng lặp các hành vi (actions)
		exists := false
		for _, v := range groupMap[key] {
			if v == behavior {
				exists = true
				break
			}
		}
		if !exists {
			groupMap[key] = append(groupMap[key], behavior)
		}
	}

	var navigation []iamEntity.NavigationItem
	for k, actions := range groupMap {
		navigation = append(navigation, iamEntity.NavigationItem{
			Key:     k,
			Actions: actions,
		})
	}

	return &iamEntity.RenderContext{
		Navigation:   navigation,
		Capabilities: capabilities,
	}, nil
}
