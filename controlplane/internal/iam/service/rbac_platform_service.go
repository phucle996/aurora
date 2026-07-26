package iamSvcImpl

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"controlplane/internal/cacheengine"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamproto "controlplane/internal/iam/transport/rpc/proto"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: RbacPlatformService thực thi interface quản lý vai trò và phân quyền cấp hệ thống (platform)
type RbacPlatformService struct {
	repo        iamRepoInterface.RbacPlatformRepository
	tenantRepo  iamRepoInterface.RbacTenantRepository
	cacheEngine *cacheengine.CacheRegistry
	authRedis   *goredis.Client
	sharedRedis *goredis.Client
}

// [COMMENT]: NewRbacPlatformService khởi tạo một thể hiện mới của RbacPlatformService
func NewRbacPlatformService(
	repo iamRepoInterface.RbacPlatformRepository,
	tenantRepo iamRepoInterface.RbacTenantRepository,
	cacheEngine *cacheengine.CacheRegistry,
	authRedis *goredis.Client,
	sharedRedis *goredis.Client,
) iamSvcInterface.RbacPlatformService {
	return &RbacPlatformService{
		repo:        repo,
		tenantRepo:  tenantRepo,
		cacheEngine: cacheEngine,
		authRedis:   authRedis,
		sharedRedis: sharedRedis,
	}
}

// [COMMENT]: AssignUserRole gán vai trò, fence Auth Redis và fan-out L1 invalidation qua Shared Redis.
func (s *RbacPlatformService) AssignUserRole(ctx context.Context, callerLevel uint8, userID uuid.UUID, roleID uuid.UUID) error {
	// [COMMENT]: 1. Gọi Repo để thực hiện gán và cập nhật DB (với check phân cấp CTE)
	if err := s.repo.AssignUserRole(ctx, callerLevel, userID, roleID); err != nil {
		return err
	}

	// [COMMENT]: 2. Thu hồi cache user_role của target user trên L1 cục bộ
	s.cacheEngine.L1.Delete("user_role:" + userID.String())

	// [COMMENT]: 3. Tăng generation và xóa snapshot trong một Lua call để reader không ghi lại dữ liệu cũ sau race.
	var invalidationErr error
	if s.authRedis != nil {
		tag := "authz:billing:{" + userID.String() + "}"
		if err := s.authRedis.Eval(ctx, `
			redis.call("INCR", KEYS[1])
			redis.call("EXPIRE", KEYS[1], ARGV[1])
			redis.call("DEL", KEYS[2], KEYS[3])
			return 1
		`, []string{tag + ":generation", tag + ":data", tag + ":data_generation"}, int64(86400)).Err(); err != nil {
			invalidationErr = fmt.Errorf("invalidate Billing authorization cache after role assignment: %w", err)
		}
	}

	// [COMMENT]: Shared Redis Pub/Sub chỉ fan-out xóa L1; Auth Redis generation ở trên mới là correctness fence.
	if invalidationErr == nil && s.sharedRedis != nil {
		if err := s.sharedRedis.Publish(ctx, "authz.invalidate.billing", userID.String()).Err(); err != nil {
			invalidationErr = fmt.Errorf("publish Billing authorization invalidation: %w", err)
		}
	}

	return invalidationErr
}

// [COMMENT]: AssignTenantRole gán vai trò platform cho tenant (skeleton)
func (s *RbacPlatformService) AssignTenantRole(ctx context.Context, tenantRole *iamEntity.TenantRole) error {
	// [COMMENT]: Sẽ hiện thực hóa ở phase tiếp theo
	return nil
}

// [COMMENT]: ListPlatformRoles trả về danh sách vai trò hệ thống có level thấp hơn caller
func (s *RbacPlatformService) ListPlatformRoles(ctx context.Context, callerLevel uint8) ([]iamEntity.Role, error) {
	return s.repo.ListPlatformRoles(ctx, callerLevel)
}

// [COMMENT]: CreateRole tạo vai trò hệ thống mới và map permissions kèm kiểm tra sở hữu tập con quyền của caller
func (s *RbacPlatformService) CreateRole(ctx context.Context, callerUserID uuid.UUID, role *iamEntity.Role, permissionIDs []uuid.UUID) error {
	role.ID = uuid.New()
	return s.repo.CreateRole(ctx, callerUserID, role, permissionIDs)
}

// [COMMENT]: ListPermissions lấy danh sách permissions catalog hệ thống được lọc theo quyền của caller
func (s *RbacPlatformService) ListPermissions(ctx context.Context, callerUserID uuid.UUID) ([]iamEntity.Permission, error) {
	return s.repo.ListPermissions(ctx, callerUserID)
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
func (s *RbacPlatformService) GetRenderContext(
	ctx context.Context,
	userID uuid.UUID,
	tenantID uuid.UUID,
) (*iamEntity.RenderContext, error) {
	var entry *iamproto.RoleEntry
	if tenantID != uuid.Nil {
		// [COMMENT]: Tenant render context is resolved from the exact active
		// membership role; platform permissions must not leak into tenant UI.
		binaryEntry, err := s.tenantRepo.GetUserTenantRolePermissions(ctx, userID, tenantID)
		if err != nil {
			return nil, fmt.Errorf("rbac platform service: get tenant render permissions: %w", err)
		}
		entry = &iamproto.RoleEntry{}
		if err := proto.Unmarshal(binaryEntry, entry); err != nil {
			return nil, fmt.Errorf("rbac platform service: decode tenant render permissions: %w", err)
		}
	} else {
		// [COMMENT]: Personal/platform role keeps the existing bounded cache.
		val, err := s.cacheEngine.GetOrLoad(ctx, "user_role", userID.String())
		if err != nil {
			return nil, fmt.Errorf("rbac platform service: get user render permissions: %w", err)
		}
		var ok bool
		entry, ok = val.(*iamproto.RoleEntry)
		if !ok || entry == nil {
			return &iamEntity.RenderContext{
				Navigation:   []iamEntity.NavigationItem{},
				Capabilities: map[string]bool{},
				IsPersonal:   true,
			}, nil
		}
	}

	// [COMMENT]: 2. Trích xuất danh sách permissions thô (permissions string)
	rawPerms := entry.Permissions

	capabilities := make(map[string]bool)
	groupMap := make(map[string][]string)
	isPersonal := tenantID == uuid.Nil

	for _, p := range rawPerms {
		capabilities[p] = true

		parts := strings.Split(p, ":")
		// [COMMENT]: RBAC Policy tuân thủ cấu trúc 5 bậc (Identity:Workspace:Module:Object:Behavior)
		// định nghĩa trong rbac_god_view_workflow.md.
		if len(parts) != 5 {
			continue
		}

		// [COMMENT]: Key chỉ chứa Module và Object (Module:Object) để giấu sạch Identity và Workspace ID
		// tránh rò rỉ dữ liệu nhạy cảm lên Client/Frontend.
		key := parts[2] + ":" + parts[3]
		behavior := parts[4]

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

	keys := make([]string, 0, len(groupMap))
	for key := range groupMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	navigation := make([]iamEntity.NavigationItem, 0, len(keys))
	for _, key := range keys {
		actions := groupMap[key]
		sort.Strings(actions)
		navigation = append(navigation, iamEntity.NavigationItem{
			Key:     key,
			Actions: actions,
		})
	}

	return &iamEntity.RenderContext{
		Navigation:   navigation,
		Capabilities: capabilities,
		IsPersonal:   isPersonal,
	}, nil
}

// [COMMENT]: DeleteRolePlatform thực hiện xóa vai trò platform thông qua repository
func (s *RbacPlatformService) DeleteRolePlatform(ctx context.Context, callerLevel uint8, roleID uuid.UUID) error {
	return s.repo.DeleteRolePlatform(ctx, callerLevel, roleID)
}

// [COMMENT]: GetRoleDetails lấy chi tiết một vai trò platform cùng danh sách đối tượng permission bậc 3
func (s *RbacPlatformService) GetRoleDetails(ctx context.Context, callerLevel uint8, roleID uuid.UUID) (*iamEntity.Role, []iamEntity.Permission, error) {
	return s.repo.GetRoleDetails(ctx, callerLevel, roleID)
}

// [COMMENT]: UpdateRole cập nhật role rồi fence/fan-out authorization cho toàn bộ user bị ảnh hưởng.
func (s *RbacPlatformService) UpdateRole(ctx context.Context, callerUserID uuid.UUID, callerLevel uint8, input *iamEntity.UpdateRoleInput) error {
	affectedUserIDs, err := s.repo.UpdateRole(ctx, callerUserID, callerLevel, input)
	if err != nil {
		return err
	}

	// [COMMENT]: Thu hồi L1 cục bộ, fence Auth Redis rồi fan-out qua Shared Redis cho từng user.
	var invalidationErr error
	for _, uID := range affectedUserIDs {
		s.cacheEngine.L1.Delete("user_role:" + uID.String())
		userInvalidationSucceeded := true
		if s.authRedis != nil {
			tag := "authz:billing:{" + uID.String() + "}"
			if err := s.authRedis.Eval(ctx, `
				redis.call("INCR", KEYS[1])
				redis.call("EXPIRE", KEYS[1], ARGV[1])
				redis.call("DEL", KEYS[2], KEYS[3])
				return 1
			`, []string{tag + ":generation", tag + ":data", tag + ":data_generation"}, int64(86400)).Err(); err != nil {
				userInvalidationSucceeded = false
				if invalidationErr == nil {
					invalidationErr = fmt.Errorf("invalidate Billing authorization cache after role update: %w", err)
				}
			}
		}
		if userInvalidationSucceeded && s.sharedRedis != nil {
			if err := s.sharedRedis.Publish(ctx, "authz.invalidate.billing", uID.String()).Err(); err != nil {
				if invalidationErr == nil {
					invalidationErr = fmt.Errorf("publish Billing authorization invalidation: %w", err)
				}
			}
		}
	}

	return invalidationErr
}
