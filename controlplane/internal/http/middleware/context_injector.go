package middleware

import (
	"controlplane/pkg/logger"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Context Keys cho Remote IP và User Agent
type RemoteIPKeyType struct{}
type UserAgentKeyType struct{}

var (
	RemoteIPKey  = RemoteIPKeyType{}
	UserAgentKey = UserAgentKeyType{}
)

// Context Keys cho các giá trị parse sẵn được lưu trữ trong Gin Context Keys
const (
	CtxUserID         = "ctx_user_id"
	CtxUserLevel      = "ctx_user_level"
	CtxZoneID         = "ctx_zone_id"
	CtxTenantID       = "ctx_tenant_id"
	CtxWorkspaceID    = "ctx_workspace_id"
	CtxUserName       = "ctx_username"
	CtxUserRoleID     = "ctx_user_role_id"
	CtxClientDeviceID = "ctx_client_device_id"
)

// ContextInjector là middleware toàn cục thực hiện đọc, parse và lưu trữ tất cả các
// thông tin định danh (User ID, Role ID, Tenant ID...) từ headers (được inject bởi ACR)
// trực tiếp vào Gin Context để tối ưu hóa bộ nhớ và hiệu năng xử lý (CPU) cho các logic phía sau.
func ContextInjector() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. User Level (X-User-Level)
		if levelStr := strings.TrimSpace(c.GetHeader("X-User-Level")); levelStr != "" {
			if level, err := strconv.ParseUint(levelStr, 10, 8); err == nil {
				c.Set(CtxUserLevel, uint8(level))
			} else {
				c.Set(CtxUserLevel, err)
			}
		}

		// 2. User ID (X-User-ID)
		if idStr := strings.TrimSpace(c.GetHeader("X-User-ID")); idStr != "" {
			if id, err := uuid.Parse(idStr); err == nil {
				c.Set(CtxUserID, id)
				// The edge has already authenticated this identity. Downstream logs
				// receive the verified actor, never a client-supplied body field.
				c.Set(logger.KeyActorID, idStr)
			} else {
				c.Set(CtxUserID, err)
			}
		}

		// 3. Zone ID (X-Zone-ID)
		if idStr := strings.TrimSpace(c.GetHeader("X-Zone-ID")); idStr != "" {
			if id, err := uuid.Parse(idStr); err == nil {
				c.Set(CtxZoneID, id)
			} else {
				c.Set(CtxZoneID, err)
			}
		}

		// 4. Tenant ID (X-Tenant-ID)
		if idStr := strings.TrimSpace(c.GetHeader("X-Tenant-ID")); idStr != "" {
			if strings.EqualFold(idStr, "platform") {
				// [COMMENT]: `platform` is the verified personal sentinel, not
				// a malformed tenant UUID. Absence in context means personal.
			} else if id, err := uuid.Parse(idStr); err == nil {
				c.Set(CtxTenantID, id)
				c.Set(logger.KeyTenantID, id.String())
			} else {
				c.Set(CtxTenantID, err)
			}
		}

		// 5. Workspace ID (X-Workspace-ID)
		if idStr := strings.TrimSpace(c.GetHeader("X-Workspace-ID")); idStr != "" {
			if id, err := uuid.Parse(idStr); err == nil {
				c.Set(CtxWorkspaceID, id)
				c.Set(logger.KeyWorkspaceID, id.String())
			} else {
				c.Set(CtxWorkspaceID, err)
			}
		}

		// 6. Username (X-User-Name)
		if username := strings.TrimSpace(c.GetHeader("X-User-Name")); username != "" {
			c.Set(CtxUserName, username)
		}

		// 7. Role ID (X-User-Role-ID)
		if idStr := strings.TrimSpace(c.GetHeader("X-User-Role-ID")); idStr != "" {
			if id, err := uuid.Parse(idStr); err == nil {
				c.Set(CtxUserRoleID, id)
			} else {
				c.Set(CtxUserRoleID, err)
			}
		}

		// 8. Device ID (X-Client-Device-ID)
		if idStr := strings.TrimSpace(c.GetHeader("X-Client-Device-ID")); idStr != "" {
			if id, err := uuid.Parse(idStr); err == nil {
				c.Set(CtxClientDeviceID, id)
			} else {
				c.Set(CtxClientDeviceID, err)
			}
		}

		c.Next()
	}
}
