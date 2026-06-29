package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"controlplane/internal/cacheengine"
	iamproto "controlplane/internal/iam/transport/rpc/proto"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

// InitCacheEngine khởi tạo toàn bộ hạ tầng Cache Engine (L1, L2, Fanout, Exec) tập trung.
// Trả về thực thể CacheRegistry hợp nhất và lỗi (nếu có) phục vụ chiến lược Fail-Close.
func InitCacheEngine(rdb *goredis.Client, channel string) (*cacheengine.CacheRegistry, error) {
	if rdb == nil {
		return nil, fmt.Errorf("cacheengine: redis client is required")
	}

	// 1. Khởi tạo L1 Cache (In-memory sharded cache)
	l1Cache := cacheengine.NewL1Cache()
	if l1Cache == nil {
		return nil, fmt.Errorf("cacheengine: failed to initialize L1 cache")
	}

	// 2. Khởi tạo Facade Registry quản lý các sub-packages
	registry := cacheengine.NewCacheRegistry(l1Cache)
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

	// 6. Đăng ký tĩnh loader cho "rbac_role" phục vụ phân quyền RBAC (sử dụng GetPermissionCodesByRoleCode tối ưu hơn)
	cacheengine.Register(registry, "rbac_role", 15*time.Minute, func(ctx context.Context, param string) (*iamproto.RoleEntry, error) {
		perms, err := modules.IAM.RbacRepository.GetPermissionCodesByRoleCode(ctx, param)
		if err != nil {
			return nil, err
		}
		return &iamproto.RoleEntry{Permissions: perms}, nil
	})

	// Loader cho "rbac:user:permissions" phục vụ gộp và phân giải quyền theo scope của User
	cacheengine.Register(registry, "rbac:user:permissions", 15*time.Minute, func(ctx context.Context, param string) ([]string, error) {
		userID, err := uuid.Parse(param)
		if err != nil {
			return nil, err
		}
		return modules.IAM.RbacRepository.GetUserPermissionsMerged(ctx, userID)
	})

	// Loader cho "tenant_code_by_id" để phân giải Tenant UUID sang Tenant Code
	cacheengine.Register(registry, "tenant_code_by_id", 1*time.Hour, func(ctx context.Context, param string) (string, error) {
		tenantID, err := uuid.Parse(param)
		if err != nil {
			return "", err
		}
		return modules.IAM.RbacRepository.GetTenantCodeByID(ctx, tenantID)
	})

	// 9. Đăng ký tĩnh loader cho "zone_backpressure" phục vụ đọc-xuyên-thấu L2 Redis khi L1 RAM cache bị miss
	cacheengine.Register(registry, "zone_backpressure", 30*time.Second, func(ctx context.Context, param string) (map[string]interface{}, error) {
		key := "zone_backpressure:" + param
		payload, _, exists, err := registry.L2.Get(ctx, key)
		if err != nil {
			return nil, err
		}
		if !exists {
			return map[string]interface{}{
				"zone_id":   param,
				"congested": false,
			}, nil
		}
		var result map[string]interface{}
		if err := json.Unmarshal(payload, &result); err != nil {
			return nil, err
		}
		return result, nil
	})
}
