package middleware

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"controlplane/internal/cacheengine"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	apires "controlplane/pkg/apires"
	"controlplane/pkg/constant"

	"github.com/gin-gonic/gin"
)

var ErrRoleNotFound = errors.New("role not found")

// SystemRoleRegistry theo dõi các vai trò nào là do hệ thống định nghĩa.
// Sử dụng bản đồ thread-safe để tra cứu O(1).
type SystemRoleRegistry struct {
	mu    sync.RWMutex
	roles map[string]struct{}
}

// Thể hiện toàn cục duy nhất của SystemRoleRegistry.
var GlobalSystemRoleRegistry = &SystemRoleRegistry{
	roles: make(map[string]struct{}),
}

// RegisterSystemRole đăng ký một vai trò là vai trò hệ thống.
func RegisterSystemRole(roleCode string) {
	GlobalSystemRoleRegistry.mu.Lock()
	defer GlobalSystemRoleRegistry.mu.Unlock()
	GlobalSystemRoleRegistry.roles[strings.ToLower(strings.TrimSpace(roleCode))] = struct{}{}
}

// IsSystemRole kiểm tra xem roleCode có thuộc vai trò hệ thống hay không.
func IsSystemRole(roleCode string) bool {
	GlobalSystemRoleRegistry.mu.RLock()
	defer GlobalSystemRoleRegistry.mu.RUnlock()
	_, ok := GlobalSystemRoleRegistry.roles[strings.ToLower(strings.TrimSpace(roleCode))]
	return ok
}

// Authorize kiểm tra xem vai trò hiện tại của Actor có được phép thực hiện hành động hay không.
// Tự động phân nhánh L1 (System roles) vs L2 (Custom roles) dựa trên registry.
func Authorize(requiredPermission string, cacheEngine *cacheengine.CacheRegistry) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		// 1. Trích xuất RoleCode từ Go Context (được inject từ Access Middleware)
		roleCodeVal := ctx.Value(constant.ContextKeyRole)
		roleCode, _ := roleCodeVal.(string)
		roleCode = strings.TrimSpace(roleCode)
		if roleCode == "" {
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}

		var permissions []string
		var err error

		// 2. Phân nhánh lấy quyền: System (L1 cache) vs Custom (Lazy load)
		if IsSystemRole(roleCode) {
			// System role: Warm-up sẵn ở L1, đọc trực tiếp từ L1 cache
			cacheKey := fmt.Sprintf("rbac_role:%s", roleCode)
			val, ok := cacheEngine.L1.Get(cacheKey)
			if ok && val != nil {
				// Ép kiểu ngược lại từ L1Envelope để lấy dữ liệu Value gốc
				if envelope, ok := val.(*cacheengine.L1Envelope); ok && envelope != nil {
					if protoEntry, ok := envelope.Value.(*iamproto.RoleEntry); ok && protoEntry != nil {
						permissions = protoEntry.Permissions
					} else if entry, ok := envelope.Value.(iamSvcInterface.RoleEntry); ok {
						permissions = entry.Permissions
					} else if pEntry, ok := envelope.Value.(*iamSvcInterface.RoleEntry); ok && pEntry != nil {
						permissions = pEntry.Permissions
					}
				}
			} else {
				err = ErrRoleNotFound
			}
		} else {
			// Custom role: Lazy-load thông qua CacheRegistry GetOrLoad
			var val any
			val, err = cacheEngine.GetOrLoad(ctx, "rbac_role", roleCode)
			if err == nil && val != nil {
				if protoEntry, ok := val.(*iamproto.RoleEntry); ok && protoEntry != nil {
					permissions = protoEntry.Permissions
				} else if entry, ok := val.(iamSvcInterface.RoleEntry); ok {
					permissions = entry.Permissions
				} else if pEntry, ok := val.(*iamSvcInterface.RoleEntry); ok && pEntry != nil {
					permissions = pEntry.Permissions
				}
			}
		}

		// Nếu xảy ra lỗi hoặc không tìm thấy vai trò
		if err != nil || len(permissions) == 0 {
			if errors.Is(err, ErrRoleNotFound) || len(permissions) == 0 {
				apires.RespondForbidden(c, "role not found or has no permissions")
			} else {
				apires.RespondServiceUnavailable(c, "authorization temporarily unavailable")
			}
			c.Abort()
			return
		}

		// 3. Kiểm tra permission yêu cầu
		hasPermission := false
		target := strings.ToLower(strings.TrimSpace(requiredPermission))
		for _, p := range permissions {
			if strings.EqualFold(strings.TrimSpace(p), target) {
				hasPermission = true
				break
			}
		}

		if !hasPermission {
			apires.RespondForbidden(c, "permission denied")
			c.Abort()
			return
		}

		c.Next()
	}
}
