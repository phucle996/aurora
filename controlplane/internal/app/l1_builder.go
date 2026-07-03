package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"controlplane/internal/cacheengine"
	iamproto "controlplane/internal/iam/transport/rpc/proto"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
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

	// 6. Đăng ký loader cho "rbac_role" phục vụ phân quyền RBAC theo mô hình ID-based 5 cấp.
	//
	// Hai dạng param:
	//   - Nhánh Tenant:   "<role_id>:<tenant_id>:<workspace_id>"
	//   - Nhánh Personal: "personal:<user_id>:<workspace_id>"
	//
	// DB đã lưu sẵn full 5-part key nên repo trả về danh sách key hoàn chỉnh.
	// Loader chỉ cần gọi đúng repo method và đóng gói kết quả vào binary Protobuf.
	cacheengine.Register(registry, "rbac_role", 15*time.Minute, func(ctx context.Context, param string) (*iamproto.RoleEntry, error) {
		// [COMMENT]: Phân tách tham số param thành 3 phần ngăn cách bởi ":"
		parts := strings.SplitN(param, ":", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("rbac_role loader: invalid param format %q, expected <role_id|'personal'>:<id>:<workspace_id>", param)
		}

		var binaryData []byte
		var err error

		if strings.EqualFold(parts[0], "personal") {
			// [COMMENT]: Nhánh Personal — trích xuất user_id và workspace_id để query trực tiếp từ user_role
			userID, parseErr := uuid.Parse(parts[1])
			if parseErr != nil {
				return nil, fmt.Errorf("rbac_role loader: invalid user_id %q: %w", parts[1], parseErr)
			}
			workspaceID, parseErr := uuid.Parse(parts[2])
			if parseErr != nil {
				return nil, fmt.Errorf("rbac_role loader: invalid workspace_id %q: %w", parts[2], parseErr)
			}

			// [COMMENT]: Lấy raw binary permissions từ DB thông qua repository
			binaryData, err = modules.IAM.RbacRepository.GetUserRolePermissions(ctx, userID, workspaceID)
			if err != nil {
				return nil, fmt.Errorf("rbac_role loader: load user role permissions: %w", err)
			}
		} else {
			// [COMMENT]: Nhánh Tenant — trích xuất role_id, tenant_id, và workspace_id
			roleID, parseErr := uuid.Parse(parts[0])
			if parseErr != nil {
				return nil, fmt.Errorf("rbac_role loader: invalid role_id %q: %w", parts[0], parseErr)
			}
			tenantID, parseErr := uuid.Parse(parts[1])
			if parseErr != nil {
				return nil, fmt.Errorf("rbac_role loader: invalid tenant_id %q: %w", parts[1], parseErr)
			}
			workspaceID, parseErr := uuid.Parse(parts[2])
			if parseErr != nil {
				return nil, fmt.Errorf("rbac_role loader: invalid workspace_id %q: %w", parts[2], parseErr)
			}

			// [COMMENT]: Lấy raw binary permissions của tenant từ DB
			binaryData, err = modules.IAM.RbacRepository.GetTenantRolePermissions(ctx, tenantID, workspaceID, roleID)
			if err != nil {
				return nil, fmt.Errorf("rbac_role loader: load tenant role permissions: %w", err)
			}
		}

		// [COMMENT]: Nếu không có dữ liệu binary trả về, trả về RoleEntry rỗng để tránh lỗi nil pointer
		if len(binaryData) == 0 {
			return &iamproto.RoleEntry{Permissions: []string{}}, nil
		}

		// [COMMENT]: Giải mã raw binary bytes (Protobuf format) thành đối tượng RoleEntry để lưu trữ trên L1 cache RAM
		var roleEntry iamproto.RoleEntry
		if err := proto.Unmarshal(binaryData, &roleEntry); err != nil {
			return nil, fmt.Errorf("rbac_role loader: failed to unmarshal binary role entry: %w", err)
		}

		return &roleEntry, nil
	})

}
