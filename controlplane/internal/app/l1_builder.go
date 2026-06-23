package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"controlplane/internal/cacheengine"
	coreEntity "controlplane/internal/core/domain/entity"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"controlplane/internal/security"

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

	// 3. Đăng ký tĩnh loader cho "zone_by_code" sử dụng generic function
	cacheengine.Register(registry, "zone_by_code", 5*time.Minute, func(ctx context.Context, param string) (string, error) {
		zoneID, err := modules.Core.ZoneRepository.GetZoneIDByCode(ctx, param)
		return zoneID.String(), err
	})

	// 3b. Đăng ký tĩnh loader cho "zone_status_by_id" để kiểm tra trạng thái Zone theo thời gian thực (real-time status check)
	cacheengine.Register(registry, "zone_status_by_id", 5*time.Minute, func(ctx context.Context, param string) (string, error) {
		zoneUUID, err := uuid.Parse(param)
		if err != nil {
			return "", err
		}
		zone, err := modules.Core.ZoneRepository.GetZoneByID(ctx, zoneUUID)
		if err != nil {
			return "", err
		}
		return string(zone.Status), nil
	})

	// 4b. Đăng ký tĩnh loader cho danh sách "zone_list" chứa cả ID và Code để sync gRPC sang ACL
	cacheengine.Register(registry, "zone_list", 10*time.Minute, func(ctx context.Context, param string) ([]coreEntity.Zone, error) {
		// [COMMENT]: Truy cập DB lấy toàn bộ list zone, cache 10 phút.
		return modules.Core.ZoneRepository.ListZones(ctx)
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
		vaultClient := security.GetVaultClient()
		if vaultClient == nil {
			return "", fmt.Errorf("vault client not initialized")
		}

		secret, err := vaultClient.Logical().Read("secret/data/admin/2fa-secret")
		if err != nil {
			return "", fmt.Errorf("failed to read admin 2fa secret from vault: %w", err)
		}
		if secret == nil || secret.Data == nil {
			return "", fmt.Errorf("admin 2fa secret not found in vault")
		}

		dataMap, ok := secret.Data["data"].(map[string]interface{})
		if !ok || dataMap == nil {
			return "", fmt.Errorf("invalid data format in vault secret")
		}

		plainSecret, ok := dataMap["secret"].(string)
		if !ok || plainSecret == "" {
			return "", fmt.Errorf("secret not found in vault secret")
		}

		return plainSecret, nil
	})

	// 8. Đăng ký tĩnh loader cho "admin_api_key_active"
	cacheengine.Register(registry, "admin_api_key_active", 24*time.Hour, func(ctx context.Context, param string) (string, error) {
		// [COMMENT]: Khởi trị L1 cache miss: lazy load khóa gốc từ Vault, băm SHA256 rồi lưu RAM, hủy plaintext
		vaultClient := security.GetVaultClient()
		if vaultClient == nil {
			return "", fmt.Errorf("vault client not initialized")
		}

		secret, err := vaultClient.Logical().Read("secret/data/admin/api-key")
		if err != nil {
			return "", fmt.Errorf("failed to read admin api key from vault: %w", err)
		}
		if secret == nil || secret.Data == nil {
			return "", fmt.Errorf("active admin api key not found in vault")
		}

		dataMap, ok := secret.Data["data"].(map[string]interface{})
		if !ok || dataMap == nil {
			return "", fmt.Errorf("invalid data format in vault secret")
		}

		plainKey, ok := dataMap["api_key"].(string)
		if !ok || plainKey == "" {
			return "", fmt.Errorf("api_key not found in vault secret")
		}

		// [COMMENT]: Chỉ cache SHA-256 hash của API key để đảm bảo an toàn tuyệt đối
		hashKey := security.HashTokenSHA256(plainKey)
		return hashKey, nil
	})

	// 8b. Đăng ký tĩnh loader cho "admin_public_key"
	cacheengine.Register(registry, "admin_public_key", 24*time.Hour, func(ctx context.Context, param string) (string, error) {
		vaultClient := security.GetVaultClient()
		if vaultClient == nil {
			return "", fmt.Errorf("vault client not initialized")
		}

		secret, err := vaultClient.Logical().Read("secret/data/admin/public-key")
		if err != nil {
			return "", fmt.Errorf("failed to read admin public key from vault: %w", err)
		}
		if secret == nil || secret.Data == nil {
			return "", fmt.Errorf("admin public key not found in vault")
		}

		dataMap, ok := secret.Data["data"].(map[string]interface{})
		if !ok || dataMap == nil {
			return "", fmt.Errorf("invalid data format in vault secret")
		}

		pubKey, ok := dataMap["public_key"].(string)
		if !ok || pubKey == "" {
			return "", fmt.Errorf("public_key not found in vault secret")
		}

		return pubKey, nil
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
