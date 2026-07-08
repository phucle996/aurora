package iamSvcImpl

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"controlplane/internal/cacheengine"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamproto "controlplane/internal/iam/transport/rpc/proto"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
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

// [COMMENT]: GetUserRolePermissions lấy danh sách permissions binary của user trên tất cả workspaces theo user id
func (s *RbacService) GetUserRolePermissions(ctx context.Context, userID uuid.UUID) ([]byte, error) {
	// [COMMENT]: Sử dụng cacheengine registry và loader "user_role" với key là userID
	val, err := s.cacheEngine.GetOrLoad(ctx, "user_role", userID.String())
	if err != nil {
		return nil, fmt.Errorf("rbac service: get or load user role permissions from cache: %w", err)
	}

	roleEntry, ok := val.(*iamproto.RoleEntry)
	if !ok {
		return nil, errors.New("rbac service: cached item is not of type *RoleEntry")
	}

	// [COMMENT]: Marshal ngược struct sang binary để khớp interface []byte
	bytes, err := proto.Marshal(roleEntry)
	if err != nil {
		return nil, fmt.Errorf("rbac service: marshal role entry to binary: %w", err)
	}
	return bytes, nil
}

// [COMMENT]: GetRenderContext sinh cấu hình Navigation và Capabilities từ bytes RBAC L1 cache theo user id
func (s *RbacService) GetRenderContext(ctx context.Context, userID uuid.UUID) (*iamEntity.RenderContext, error) {
	// [COMMENT]: Lấy danh sách permissions của user thông qua key user_role:<userID>
	val, err := s.cacheEngine.GetOrLoad(ctx, "user_role", userID.String())
	if err != nil {
		return nil, fmt.Errorf("rbac service: get render context from cache: %w", err)
	}

	roleEntry, ok := val.(*iamproto.RoleEntry)
	if !ok {
		return nil, errors.New("rbac service: cached item is not of type *RoleEntry")
	}

	// [COMMENT]: Map gom nhóm permissions theo 4 phần đầu tiên
	groupMap := make(map[string][]string)
	capabilities := make(map[string]bool)

	for _, p := range roleEntry.Permissions {
		// [COMMENT]: Thay thế platform-wide nil UUID ("00000000-0000-0000-0000-000000000000")
		// thành ký tự wildcard "*" giúp payload trả về Client (Navigation & Capabilities) sạch sẽ và thân thiện hơn.
		pClean := strings.ReplaceAll(p, "00000000-0000-0000-0000-000000000000", "*")
		capabilities[pClean] = true

		if pClean == "*" || pClean == "*:*:*" || pClean == "*:*" {
			continue
		}

		parts := strings.Split(pClean, ":")
		if len(parts) < 5 {
			continue
		}

		key := strings.Join(parts[0:4], ":")
		behavior := parts[4]

		// Tránh add behavior trùng lặp
		exists := false
		for _, b := range groupMap[key] {
			if b == behavior {
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

// [COMMENT]: ListPlatformRoles lấy toàn bộ danh sách roles có scope là platform
func (s *RbacService) ListPlatformRoles(ctx context.Context) ([]iamEntity.Role, error) {
	return s.repo.ListPlatformRoles(ctx)
}

// [COMMENT]: ListTenantRoles lấy danh sách roles gán cho tenant cụ thể
func (s *RbacService) ListTenantRoles(ctx context.Context, tenantID uuid.UUID) ([]iamEntity.Role, error) {
	return s.repo.ListTenantRoles(ctx, tenantID)
}

// [COMMENT]: CreateRole tạo một vai trò mới kèm theo gán permissions
func (s *RbacService) CreateRole(ctx context.Context, role *iamEntity.Role, permissionIDs []uuid.UUID) error {
	role.ID = uuid.New()
	return s.repo.CreateRole(ctx, role, permissionIDs)
}

// [COMMENT]: ListPermissions lấy toàn bộ danh sách permissions có trong hệ thống
func (s *RbacService) ListPermissions(ctx context.Context) ([]iamEntity.Permission, error) {
	return s.repo.ListPermissions(ctx)
}
