package middleware

import (
	"errors"
	"strings"
	"sync"

	"controlplane/internal/cacheengine"
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

// Authorize kiểm tra xem Actor hiện tại có đủ quyền thực hiện hành động theo scope yêu cầu hay không.
// requiredPermission template format: "<scope_type>:<module>:<object>:<behavior>",
//
//	ví dụ: "tenant:hierarchy:tenant-member:delete"
func Authorize(requiredPermission string, cacheEngine *cacheengine.CacheRegistry) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		// 1. Trích xuất User ID từ Go Context (được inject từ Access/Auth Middleware)
		var userID string
		ident, ok := ctx.Value(constant.IdentityKey).(*constant.Identity)
		if !ok || ident == nil || ident.UserID == "" {
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}
		userID = ident.UserID

		// Root/System bypass để nâng cao hiệu năng hệ thống quản trị tối cao
		if strings.EqualFold(ident.Role, "platform_root") {
			c.Next()
			return
		}

		// 2. Load toàn bộ danh sách các quyền đã gộp và gắn prefix của user từ L1/L2 cache
		val, err := cacheEngine.GetOrLoad(ctx, "rbac:user:permissions", userID)
		if err != nil || val == nil {
			apires.RespondForbidden(c, "role permissions not found")
			c.Abort()
			return
		}

		userPerms, ok := val.([]string)
		if !ok {
			apires.RespondInternalError(c, "invalid permissions cache type mapping")
			c.Abort()
			return
		}

		// 3. Phân rã cấu trúc permission yêu cầu
		parts := strings.SplitN(requiredPermission, ":", 2)
		if len(parts) < 2 {
			apires.RespondInternalError(c, "invalid required permission format template")
			c.Abort()
			return
		}

		scopeType := parts[0]
		action := parts[1] // Ví dụ: "hierarchy:tenant-member:delete"

		var actualPermissionToCheck string
		switch scopeType {
		case "platform":
			actualPermissionToCheck = "platform:" + action
		case "personal":
			actualPermissionToCheck = "personal:" + action
		case "tenant":
			// Phân giải mã Tenant code từ ngữ cảnh request
			tenantCode, err := resolveTenantCode(c, cacheEngine)
			if err != nil || tenantCode == "" {
				apires.RespondForbidden(c, "missing or invalid tenant context scope")
				c.Abort()
				return
			}
			actualPermissionToCheck = tenantCode + ":" + action
		default:
			apires.RespondInternalError(c, "unsupported permission scope check type")
			c.Abort()
			return
		}

		// 4. Khớp quyền (Case-Insensitive match)
		hasPermission := false
		for _, p := range userPerms {
			if strings.EqualFold(p, actualPermissionToCheck) {
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

// resolveTenantCode phân giải mã định danh tenant_code từ header, query, path, hoặc context session
func resolveTenantCode(c *gin.Context, cacheEngine *cacheengine.CacheRegistry) (string, error) {
	// 1) Đọc trực tiếp từ Custom header x-tenant-code (đã phân giải ở Envoy/ACR hoặc admin UI)
	tenantCode := c.GetHeader("x-tenant-code")
	if tenantCode != "" {
		return strings.ToLower(strings.TrimSpace(tenantCode)), nil
	}

	// 2) Tìm x-tenant-id từ Header, query, hoặc params để lấy UUID rồi map sang code qua cache engine
	tenantIDStr := c.GetHeader("x-tenant-id")
	if tenantIDStr == "" {
		tenantIDStr = c.Query("tenant_id")
		if tenantIDStr == "" {
			tenantIDStr = c.Param("tenant_id")
		}
	}

	if tenantIDStr != "" {
		val, err := cacheEngine.GetOrLoad(c.Request.Context(), "tenant_code_by_id", tenantIDStr)
		if err == nil && val != nil {
			if code, ok := val.(string); ok {
				return strings.ToLower(code), nil
			}
		}
	}

	// 3) Fallback: lấy Tenant ID từ Identity session hiện tại được lưu trong context
	if ident, ok := c.Request.Context().Value(constant.IdentityKey).(*constant.Identity); ok && ident != nil && ident.TenantID != "" {
		val, err := cacheEngine.GetOrLoad(c.Request.Context(), "tenant_code_by_id", ident.TenantID)
		if err == nil && val != nil {
			if code, ok := val.(string); ok {
				return strings.ToLower(code), nil
			}
		}
	}

	return "", nil
}
