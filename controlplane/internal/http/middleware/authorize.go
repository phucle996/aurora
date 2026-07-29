package middleware

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"controlplane/internal/cacheengine"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	apires "controlplane/pkg/apires"
	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var ErrRoleNotFound = errors.New("role not found")

// RequireSessionProof fails closed unless ACR verified and consumed a
// session-bound Ed25519 challenge for this exact critical mutation.
func RequireSessionProof() gin.HandlerFunc {
	return func(c *gin.Context) {
		challengeID, err := uuid.Parse(strings.TrimSpace(c.GetHeader("x-session-proof-challenge-id")))
		if c.GetHeader("x-session-proof-verified") != "true" || err != nil || challengeID == uuid.Nil {
			apires.RespondForbidden(c, "verified session proof is required")
			c.Abort()
			return
		}
		c.Next()
	}
}

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
		const op = "auth.authorize"

		// 1. Trích xuất User ID từ Gin Context (nạp sẵn bởi ContextInjector)
		userID, ok := pkgcontext.GetUserID(c, op)
		if !ok {
			c.Abort()
			return
		}

		// [COMMENT]: 2.1. Kiểm tra User Level nếu requiredLevel khác "*"
		if requiredLevel != "*" {
			actorLevel, ok := pkgcontext.GetUserLevel(c, op)
			if !ok {
				c.Abort()
				return
			}
			reqLevel, err := strconv.Atoi(requiredLevel)
			if err != nil {
				logger.HandlerError(c, op, fmt.Errorf("invalid required level configuration: %w", err))
				apires.RespondInternalError(c, "invalid required level configuration")
				c.Abort()
				return
			}
			// Càng nhỏ level càng cao: Root=0, Admin=1... User=8
			if actorLevel > uint8(reqLevel) {
				logger.HandlerWarn(c, op, nil, fmt.Sprintf("insufficient level hierarchy: actor level %d > required %d", actorLevel, reqLevel))
				apires.RespondForbidden(c, "insufficient level hierarchy")
				c.Abort()
				return
			}
		}

		// 3. Trích xuất Role UUID từ Gin Context (nạp sẵn bởi ContextInjector)
		roleID, ok := pkgcontext.GetUserRoleID(c, op)
		if !ok {
			c.Abort()
			return
		}

		// 4. Validate format requiredPermission phải đúng 3 phần: <module>:<object>:<behavior>
		parts := strings.SplitN(requiredPermission, ":", 3)
		if len(parts) != 3 {
			logger.HandlerWarn(c, op, nil, fmt.Sprintf("invalid required permission format: %s", requiredPermission))
			apires.RespondInternalError(c, "invalid required permission format: must be <module>:<object>:<behavior>")
			c.Abort()
			return
		}

		// 5. Trích xuất Workspace UUID (luôn bắt buộc — user luôn có ít nhất 1 workspace)
		workspaceID, ok := pkgcontext.GetWorkspaceID(c, op)
		if !ok {
			c.Abort()
			return
		}

		// 6. Xác định cấp 1 theo nhánh và build expected key 5 cấp đầy đủ
		// Format DB đã lưu sẵn: <cấp1>:<workspace_uuid>:<module>:<object>:<behavior>
		var tenantID string
		if val, exists := c.Get(pkgcontext.CtxTenantID); exists {
			if id, ok := val.(uuid.UUID); ok {
				tenantID = id.String()
			}
		}

		var scopeCtx string
		var cacheParam string
		var cacheNamespace string

		if tenantID != "" {
			// [COMMENT]: Nhánh Tenant — cache key bậc 1: tenant_role:<role_id>:<tenant_id>
			scopeCtx = tenantID
			cacheParam = roleID.String() + ":" + tenantID
			cacheNamespace = "tenant_role"
		} else {
			// [COMMENT]: Nhánh Personal — cache key bậc 1: user_role:<userID>
			username, ok := pkgcontext.GetUserName(c, op)
			if !ok {
				c.Abort()
				return
			}
			scopeCtx = username
			cacheParam = userID.String()
			cacheNamespace = "user_role"
		}

		// 8. Tra cứu L1 cache theo namespace tương ứng
		val, err := cacheEngine.GetOrLoad(ctx, cacheNamespace, cacheParam)
		if err != nil || val == nil {
			logger.HandlerWarn(c, op, err, fmt.Sprintf("role permissions not found for cacheParam %s", cacheParam))
			apires.RespondForbidden(c, "role permissions not found")
			c.Abort()
			return
		}

		roleEntry, ok := val.(*iamproto.RoleEntry)
		if !ok {
			logger.HandlerError(c, op, fmt.Errorf("invalid permissions cache type mapping for cacheParam %s", cacheParam))
			apires.RespondInternalError(c, "invalid permissions cache type mapping")
			c.Abort()
			return
		}

		// 9. So khớp expected key hoặc wildcard platform key với danh sách quyền tĩnh đã gộp trong cache.
		expectedKey := scopeCtx + ":" + workspaceID.String() + ":" + requiredPermission
		wildcardExpectedKey := scopeCtx + ":*:" + requiredPermission

		hasPermission := false
		for _, p := range roleEntry.Permissions {
			// [COMMENT]: Chuẩn hóa platform-wide nil UUID về dạng '*' để đồng bộ so khớp wildcard với client
			pClean := strings.ReplaceAll(p, "00000000-0000-0000-0000-000000000000", "*")
			if strings.EqualFold(pClean, expectedKey) || strings.EqualFold(pClean, wildcardExpectedKey) {
				hasPermission = true
				break
			}
		}

		if !hasPermission {
			logger.HandlerWarn(c, op, nil, fmt.Sprintf("permission denied: expected %s or %s, but has not matched", expectedKey, wildcardExpectedKey))
			apires.RespondForbidden(c, "permission denied")
			c.Abort()
			return
		}

		c.Next()
	}
}
