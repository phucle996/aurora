package middleware

import (
	"errors"
	"strconv"
	"strings"

	"controlplane/internal/cacheengine"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	apires "controlplane/pkg/apires"
	"controlplane/pkg/constant"

	"github.com/gin-gonic/gin"
)

var ErrRoleNotFound = errors.New("role not found")

// Authorize kiểm tra Actor hiện tại có đủ quyền thực hiện hành động theo scope yêu cầu hay không.
//
// requiredPermission phải theo format 3 phần ngăn cách bởi ":" — chỉ phần hành động:
//
//	"<module>:<object>:<behavior>"
//
// Middleware tự động điền cấp 1 (tenant_uuid / username) và cấp 2 (workspace_uuid) từ headers
// để tạo nên expected permission key 5 cấp đầy đủ:
//
//	<tenant_uuid | username> : <workspace_uuid> : <module> : <object> : <behavior>
//
// Hai nhánh:
//   - Nhánh Tenant: X-Tenant-ID có giá trị → cấp 1 = tenant_uuid
//   - Nhánh Personal: X-Tenant-ID vắng mặt → cấp 1 = username (X-User-Name, tức sub trong JWT)
//
// Ví dụ:
//
//	Authorize("hypervisor:vps:create", cache, "2") với X-Tenant-ID="uuid-tenant" và X-Workspace-ID="uuid-ws"
//	→ expected key = "uuid-tenant:uuid-ws:hypervisor:vps:create" và checks user level <= 2
//
//	Authorize("hypervisor:vps:list", cache, "*") không có X-Tenant-ID, X-User-Name="alice", X-Workspace-ID="uuid-ws"
//	→ expected key = "alice:uuid-ws:hypervisor:vps:list" và skips level check
func Authorize(requiredPermission string, cacheEngine *cacheengine.CacheRegistry, requiredLevel string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		// 1. Trích xuất User ID từ Header (inject bởi ACR sau khi xác thực JWT)
		userID := strings.TrimSpace(c.GetHeader(constant.HeaderXUserID))
		if userID == "" {
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}

		// [COMMENT]: 2.1. Kiểm tra User Level nếu requiredLevel khác "*"
		if requiredLevel != "*" {
			userLevelStr := strings.TrimSpace(c.GetHeader(constant.HeaderXUserLevel))
			if userLevelStr == "" {
				apires.RespondForbidden(c, "missing user level context")
				c.Abort()
				return
			}
			actorLevel, err := strconv.Atoi(userLevelStr)
			if err != nil {
				apires.RespondInternalError(c, "invalid user level format")
				c.Abort()
				return
			}
			reqLevel, err := strconv.Atoi(requiredLevel)
			if err != nil {
				apires.RespondInternalError(c, "invalid required level configuration")
				c.Abort()
				return
			}
			// Càng nhỏ level càng cao: Root=0, Admin=1... User=8
			if actorLevel > reqLevel {
				apires.RespondForbidden(c, "insufficient level hierarchy")
				c.Abort()
				return
			}
		}

		// 3. Trích xuất Role UUID từ Header X-User-Role-ID (inject bởi ACR từ JWT claims)
		roleID := strings.TrimSpace(c.GetHeader(constant.HeaderXUserRoleID))
		if roleID == "" {
			apires.RespondUnauthorized(c, "missing role context")
			c.Abort()
			return
		}

		// 4. Validate format requiredPermission phải đúng 3 phần: <module>:<object>:<behavior>
		parts := strings.SplitN(requiredPermission, ":", 3)
		if len(parts) != 3 {
			apires.RespondInternalError(c, "invalid required permission format: must be <module>:<object>:<behavior>")
			c.Abort()
			return
		}

		// 5. Trích xuất Workspace UUID (luôn bắt buộc — user luôn có ít nhất 1 workspace)
		workspaceID := strings.TrimSpace(c.GetHeader(constant.HeaderXWorkspaceID))
		if workspaceID == "" {
			apires.RespondForbidden(c, "missing workspace context")
			c.Abort()
			return
		}

		// 6. Xác định cấp 1 theo nhánh và build expected key 5 cấp đầy đủ
		// Format DB đã lưu sẵn: <cấp1>:<workspace_uuid>:<module>:<object>:<behavior>
		tenantID := strings.TrimSpace(c.GetHeader(constant.HeaderXTenantID))

		var scopeCtx string   // cấp 1: tenant_uuid hoặc username
		var cacheParam string // param để tra cứu L1 cache

		if tenantID != "" {
			// [COMMENT]: Nhánh Tenant — cấp 1 là tenant_uuid
			// DB lưu key dạng: tenant_uuid:workspace_uuid:module:object:behavior
			scopeCtx = tenantID
			cacheParam = roleID + ":" + tenantID + ":" + workspaceID
		} else {
			// [COMMENT]: Nhánh Personal — cấp 1 là username (sub từ JWT, không phải user_uuid)
			// Cần cả X-User-Name để build key và X-User-ID để query DB
			username := strings.TrimSpace(c.GetHeader(constant.HeaderXUserName))
			if username == "" {
				apires.RespondForbidden(c, "missing username context for personal scope")
				c.Abort()
				return
			}
			// DB lưu key dạng: username:workspace_uuid:module:object:behavior
			scopeCtx = username
			cacheParam = "personal:" + userID + ":" + workspaceID
		}

		// 7. Build expected permission key 5 cấp đầy đủ
		expectedKey := scopeCtx + ":" + workspaceID + ":" + requiredPermission

		// 8. Tra cứu L1 cache rbac_role theo param context đã xác định
		val, err := cacheEngine.GetOrLoad(ctx, "rbac_role", cacheParam)
		if err != nil || val == nil {
			apires.RespondForbidden(c, "role permissions not found")
			c.Abort()
			return
		}

		roleEntry, ok := val.(*iamproto.RoleEntry)
		if !ok {
			apires.RespondInternalError(c, "invalid permissions cache type mapping")
			c.Abort()
			return
		}

		// 9. So khớp exact expected key với danh sách quyền tĩnh 5 cấp đã lưu sẵn trong cache.
		// DB đã build key đầy đủ nên không có bất kỳ logic runtime nào có thể bị bypass.
		hasPermission := false
		for _, p := range roleEntry.Permissions {
			if strings.EqualFold(p, expectedKey) {
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
