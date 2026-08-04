package pkgcontext

import (
	"controlplane/pkg/apires"
	"controlplane/pkg/logger"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Context Keys cho các giá trị parse sẵn được lưu trữ trong Gin Context Keys
// Khớp chính xác với các hằng số bên middleware/context_injector.go
const (
	CtxUserID         = "ctx_user_id"
	CtxUserLevel      = "ctx_user_level"
	CtxZoneID         = "ctx_zone_id"
	CtxTenantID       = "ctx_tenant_id"
	CtxWorkspaceID    = "ctx_workspace_id"
	CtxUserName       = "ctx_username"
	CtxClientDeviceID = "ctx_client_device_id"
)

// [COMMENT]: GetUserLevel trích xuất và parse level của user từ Gin Context.
// Tự động ghi log warning và trả về phản hồi lỗi HTTP nếu context trống hoặc sai định dạng.
func GetUserLevel(c *gin.Context, op string) (uint8, bool) {
	val, ok := c.Get(CtxUserLevel)
	if !ok {
		logger.HandlerWarn(c, op, nil, "missing user level context")
		apires.RespondForbidden(c, "missing user level context")
		return 0, false
	}
	if err, ok := val.(error); ok {
		logger.HandlerWarn(c, op, err, "invalid user level format")
		apires.RespondForbidden(c, "invalid user level format")
		return 0, false
	}
	if level, ok := val.(uint8); ok {
		return level, true
	}
	logger.HandlerWarn(c, op, nil, "invalid user level context type")
	apires.RespondForbidden(c, "invalid user level context")
	return 0, false
}

// [COMMENT]: GetUserID trích xuất và parse UUID của user từ Gin Context.
// Tự động ghi log warning và trả lỗi HTTP nếu thiếu hoặc sai định dạng.
func GetUserID(c *gin.Context, op string) (uuid.UUID, bool) {
	val, ok := c.Get(CtxUserID)
	if !ok {
		logger.HandlerWarn(c, op, nil, "unauthorized - missing user id context")
		apires.RespondUnauthorized(c, "unauthorized")
		return uuid.Nil, false
	}
	if err, ok := val.(error); ok {
		logger.HandlerWarn(c, op, err, "invalid x-user-id format")
		apires.RespondBadRequest(c, "invalid request")
		return uuid.Nil, false
	}
	if id, ok := val.(uuid.UUID); ok {
		return id, true
	}
	logger.HandlerWarn(c, op, nil, "invalid user id context type")
	apires.RespondUnauthorized(c, "unauthorized")
	return uuid.Nil, false
}

// [COMMENT]: GetZoneID trích xuất và parse UUID của zone từ Gin Context.
// Tự động ghi log warning và trả lỗi HTTP nếu thiếu hoặc sai định dạng.
func GetZoneID(c *gin.Context, op string) (uuid.UUID, bool) {
	val, ok := c.Get(CtxZoneID)
	if !ok {
		logger.HandlerWarn(c, op, nil, "missing zone context")
		apires.RespondBadRequest(c, "missing zone context")
		return uuid.Nil, false
	}
	if err, ok := val.(error); ok {
		logger.HandlerWarn(c, op, err, "invalid zone id format")
		apires.RespondBadRequest(c, "invalid zone id format")
		return uuid.Nil, false
	}
	if id, ok := val.(uuid.UUID); ok {
		return id, true
	}
	logger.HandlerWarn(c, op, nil, "invalid zone id context type")
	apires.RespondBadRequest(c, "missing zone context")
	return uuid.Nil, false
}

// [COMMENT]: GetTenantID trích xuất và parse UUID của tenant từ Gin Context.
// Tự động ghi log warning và trả lỗi HTTP nếu thiếu hoặc sai định dạng.
func GetTenantID(c *gin.Context, op string) (uuid.UUID, bool) {
	val, ok := c.Get(CtxTenantID)
	if !ok {
		logger.HandlerWarn(c, op, nil, "missing tenant context")
		apires.RespondBadRequest(c, "missing tenant context")
		return uuid.Nil, false
	}
	if err, ok := val.(error); ok {
		logger.HandlerWarn(c, op, err, "invalid tenant id format")
		apires.RespondBadRequest(c, "invalid tenant id format")
		return uuid.Nil, false
	}
	if id, ok := val.(uuid.UUID); ok {
		return id, true
	}
	logger.HandlerWarn(c, op, nil, "invalid tenant id context type")
	apires.RespondBadRequest(c, "missing tenant context")
	return uuid.Nil, false
}

// [COMMENT]: GetWorkspaceID trích xuất và parse UUID của workspace từ Gin Context.
// Tự động ghi log warning và trả lỗi HTTP nếu thiếu hoặc sai định dạng.
func GetWorkspaceID(c *gin.Context, op string) (uuid.UUID, bool) {
	val, ok := c.Get(CtxWorkspaceID)
	if !ok {
		logger.HandlerWarn(c, op, nil, "missing active workspace context")
		apires.RespondForbidden(c, "missing workspace context")
		return uuid.Nil, false
	}
	if err, ok := val.(error); ok {
		logger.HandlerWarn(c, op, err, "invalid workspace id format")
		apires.RespondForbidden(c, "invalid workspace id format")
		return uuid.Nil, false
	}
	if id, ok := val.(uuid.UUID); ok {
		return id, true
	}
	logger.HandlerWarn(c, op, nil, "invalid workspace id context type")
	apires.RespondForbidden(c, "missing workspace context")
	return uuid.Nil, false
}

// [COMMENT]: GetUserName trích xuất username từ Gin Context.
// Tự động ghi log warning và trả lỗi HTTP nếu thiếu.
func GetUserName(c *gin.Context, op string) (string, bool) {
	val, ok := c.Get(CtxUserName)
	if !ok {
		logger.HandlerWarn(c, op, nil, "missing username context")
		apires.RespondForbidden(c, "missing username context")
		return "", false
	}
	if username, ok := val.(string); ok && username != "" {
		return username, true
	}
	logger.HandlerWarn(c, op, nil, "invalid username context type")
	apires.RespondForbidden(c, "missing username context")
	return "", false
}

// [COMMENT]: GetClientDeviceID trích xuất và parse UUID của device đang kết nối từ Gin Context.
// Tự động ghi log warning và trả lỗi HTTP nếu thiếu hoặc sai định dạng.
func GetClientDeviceID(c *gin.Context, op string) (uuid.UUID, bool) {
	val, ok := c.Get(CtxClientDeviceID)
	if !ok {
		logger.HandlerWarn(c, op, nil, "missing device context")
		apires.RespondUnauthorized(c, "unauthorized")
		return uuid.Nil, false
	}
	if err, ok := val.(error); ok {
		logger.HandlerWarn(c, op, err, "invalid client device id format")
		apires.RespondUnauthorized(c, "unauthorized")
		return uuid.Nil, false
	}
	if id, ok := val.(uuid.UUID); ok {
		return id, true
	}
	logger.HandlerWarn(c, op, nil, "invalid client device id context type")
	apires.RespondUnauthorized(c, "unauthorized")
	return uuid.Nil, false
}

// [COMMENT]: GetTraceparent trích xuất traceparent header cho mục đích tracing telemetry (nếu có).
func GetTraceparent(c *gin.Context) string {
	return strings.TrimSpace(c.GetHeader("traceparent"))
}

// [COMMENT]: GetRequestID trích xuất request ID từ Gin Context.
func GetRequestID(c *gin.Context) string {
	if val, ok := c.Get(logger.KeyRequestID); ok {
		if s, ok := val.(string); ok && s != "" {
			return s
		}
	}
	return ""
}
