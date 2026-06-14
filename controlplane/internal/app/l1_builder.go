package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"controlplane/internal/cacheengine"
	coreEntity "controlplane/internal/core/domain/entity"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"controlplane/internal/security"

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

	// 3. Đăng ký tĩnh loader cho "zone_by_code" sử dụng generic function
	cacheengine.Register(registry, "zone_by_code", 5*time.Minute, func(ctx context.Context, param string) (string, error) {
		zoneID, err := modules.Core.ZoneRepository.GetZoneIDByCode(ctx, param)
		return zoneID.String(), err
	})

	// 4. Đăng ký tĩnh loader cho danh sách "zone_catalog" phục vụ dropdown/select UI
	cacheengine.Register(registry, "zone_catalog", 10*time.Minute, func(ctx context.Context, param string) ([]coreEntity.ZoneCatalog, error) {
		// Truy vấn trực tiếp từ Repository để tránh vòng lặp phụ thuộc (dependency loop)
		return modules.Core.ZoneRepository.GetZoneCatalog(ctx)
	})

	// 5. Đăng ký tĩnh loader cho các secret types phục vụ JWT và API key auth
	cacheengine.Register(registry, "access_secret", 1*time.Hour, func(ctx context.Context, param string) (*coreEntity.RuntimeSecrets, error) {
		return modules.Core.SecretRepository.GetAccessSecret(ctx)
	})

	cacheengine.Register(registry, "refresh_secret", 1*time.Hour, func(ctx context.Context, param string) (*coreEntity.RuntimeSecrets, error) {
		return modules.Core.SecretRepository.GetRefreshSecret(ctx)
	})

	cacheengine.Register(registry, "admin_api_key", 1*time.Hour, func(ctx context.Context, param string) (*coreEntity.RuntimeSecrets, error) {
		return modules.Core.SecretRepository.GetAdminAPIKey(ctx)
	})

	cacheengine.Register(registry, "one_time_token", 1*time.Hour, func(ctx context.Context, param string) (*coreEntity.RuntimeSecrets, error) {
		return modules.Core.SecretRepository.GetOneTimeTokenSecret(ctx)
	})

	// 6. Đăng ký tĩnh loader cho "rbac_role" phục vụ phân quyền RBAC (sử dụng GetPermissionCodesByRoleCode tối ưu hơn)
	cacheengine.Register(registry, "rbac_role", 15*time.Minute, func(ctx context.Context, param string) (*iamproto.RoleEntry, error) {
		perms, err := modules.IAM.RbacRepository.GetPermissionCodesByRoleCode(ctx, param)
		if err != nil {
			return nil, err
		}
		return &iamproto.RoleEntry{Permissions: perms}, nil
	})

	// 7. Đăng ký tĩnh loader cho "admin_2fa_secret"
	// giải mã sẵn admin 2fa secret và lưu vào l1 cache.
	cacheengine.Register(registry, "admin_2fa_secret", 5*time.Minute, func(ctx context.Context, param string) (string, error) {
		ciphertext, _, err := modules.IAM.AdminAPIKeyRepository.GetAdmin2FASecret(ctx)
		if err != nil {
			return "", err
		}
		if ciphertext == "" {
			return "", fmt.Errorf("admin 2fa secret not found")
		}
		decrypted, err := security.DecryptSecret(ciphertext)
		if err != nil {
			return "", err
		}
		return decrypted, nil
	})

	// 8. Đăng ký tĩnh loader cho "admin_api_key_active"
	cacheengine.Register(registry, "admin_api_key_active", 10*time.Second, func(ctx context.Context, param string) (*iamEntity.AdminAPIKey, error) {
		active, err := modules.IAM.AdminAPIKeyRepository.GetActiveAdminAPIKey(ctx)
		if err != nil {
			return nil, err
		}
		if active == nil {
			return nil, fmt.Errorf("active admin api key not found")
		}
		return active, nil
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
