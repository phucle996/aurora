package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"controlplane/internal/cacheengine"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"controlplane/internal/observability"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

// InitCacheEngine khởi tạo toàn bộ hạ tầng Cache Engine (L1, L2, Fanout, Exec) tập trung.
// Trả về thực thể CacheRegistry hợp nhất và lỗi (nếu có) phục vụ chiến lược Fail-Close.
func InitCacheEngine(rdb *goredis.Client, channel string, metrics observability.CacheRecorder) (*cacheengine.CacheRegistry, error) {
	if rdb == nil {
		return nil, fmt.Errorf("cacheengine: redis client is required")
	}

	// 1. Khởi tạo L1 Cache (In-memory sharded cache)
	l1Cache := cacheengine.NewL1Cache()
	if l1Cache == nil {
		return nil, fmt.Errorf("cacheengine: failed to initialize L1 cache")
	}

	// 2. Khởi tạo Facade Registry quản lý các sub-packages
	registry := cacheengine.NewCacheRegistry(l1Cache, metrics)
	if registry == nil {
		return nil, fmt.Errorf("cacheengine: failed to initialize cache registry")
	}

	// 3. Khởi tạo L2 Cache (Redis KV Cache)
	registry.L2 = cacheengine.NewL2Cache(rdb)

	// 4. Khởi tạo Fanout Invalidation Bus (Pub/Sub Sync)
	registry.Fanout = cacheengine.NewRedisFanout(rdb, channel)

	// 5. Khởi tạo Lua Executor (Atomic Redis Runner)
	registry.Exec = cacheengine.NewL2LuaExecutor(rdb)

	return registry, nil
}

// RegisterL1Loaders đăng ký toàn bộ các cache loaders tĩnh sử dụng các module nghiệp vụ đã wire hoàn chỉnh.
// Chú ý: Việc start subscription loop được trì hoãn và thực hiện độc lập tại app.go sau khi HTTP/gRPC sẵn sàng.
func RegisterL1Loaders(
	registry *cacheengine.CacheRegistry,
	modules *Modules,
) {

	// [COMMENT]: 6. Đăng ký loader cho "user_role" lưu trữ toàn bộ permissions của user theo user id
	cacheengine.Register(registry, "user_role", 15*time.Minute, func(ctx context.Context, param string) (*iamproto.RoleEntry, error) {
		userIDStr := strings.TrimSpace(param)
		if userIDStr == "" {
			return nil, fmt.Errorf("user_role loader: empty user id parameter")
		}
		userID, parseErr := uuid.Parse(userIDStr)
		if parseErr != nil {
			return nil, fmt.Errorf("user_role loader: invalid userID %q: %w", userIDStr, parseErr)
		}

		// [COMMENT]: Lấy raw binary permissions từ DB thông qua platform repository theo userID
		binaryData, err := modules.IAM.RbacPlatformRepository.GetUserRolePermissions(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("user_role loader: load user role permissions by userID: %w", err)
		}

		if len(binaryData) == 0 {
			return &iamproto.RoleEntry{Permissions: []string{}}, nil
		}

		var roleEntry iamproto.RoleEntry
		if err := proto.Unmarshal(binaryData, &roleEntry); err != nil {
			return nil, fmt.Errorf("user_role loader: failed to unmarshal binary role entry: %w", err)
		}

		return &roleEntry, nil
	})

	// [COMMENT]: 7. Đăng ký loader cho "tenant_role" lưu trữ toàn bộ permissions của tenant theo role
	cacheengine.Register(registry, "tenant_role", 15*time.Minute, func(ctx context.Context, param string) (*iamproto.RoleEntry, error) {
		parts := strings.SplitN(param, ":", 2) // <role_id>:<tenant_id>
		if len(parts) != 2 {
			return nil, fmt.Errorf("tenant_role loader: invalid param format %q, expected <role_id>:<tenant_id>", param)
		}

		roleID, parseErr := uuid.Parse(parts[0])
		if parseErr != nil {
			return nil, fmt.Errorf("tenant_role loader: invalid role_id %q: %w", parts[0], parseErr)
		}
		tenantID, parseErr := uuid.Parse(parts[1])
		if parseErr != nil {
			return nil, fmt.Errorf("tenant_role loader: invalid tenant_id %q: %w", parts[1], parseErr)
		}

		// [COMMENT]: Lấy raw binary permissions của tenant từ DB thông qua tenant repository theo tenantID và roleID
		binaryData, err := modules.IAM.RbacTenantRepository.GetTenantRolePermissions(ctx, tenantID, roleID)
		if err != nil {
			return nil, fmt.Errorf("tenant_role loader: load tenant role permissions: %w", err)
		}

		if len(binaryData) == 0 {
			return &iamproto.RoleEntry{Permissions: []string{}}, nil
		}

		var roleEntry iamproto.RoleEntry
		if err := proto.Unmarshal(binaryData, &roleEntry); err != nil {
			return nil, fmt.Errorf("tenant_role loader: failed to unmarshal binary role entry: %w", err)
		}

		return &roleEntry, nil
	})

}
