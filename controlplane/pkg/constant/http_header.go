package constant

import (
	"controlplane/pkg/apires"
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

// [COMMENT]: GetUserLevel trích xuất và parse level của user từ header X-User-Level do ACR inject.
// Tự động ghi log warning và trả về phản hồi lỗi HTTP nếu header trống hoặc sai định dạng.
func GetUserLevel(c *gin.Context, op string) (uint8, bool) {
	levelStr := strings.TrimSpace(c.GetHeader("X-User-Level"))
	if levelStr == "" {
		logger.HandlerWarn(c, op, nil, "missing user level context")
		apires.RespondForbidden(c, "missing user level context")
		return 0, false
	}
	level, err := strconv.ParseUint(levelStr, 10, 8)
	if err != nil {
		logger.HandlerWarn(c, op, err, "invalid user level format")
		apires.RespondForbidden(c, "invalid user level format")
		return 0, false
	}
	return uint8(level), true
}

// [COMMENT]: GetUserID trích xuất và parse UUID của user từ header X-User-ID do ACR inject.
// Tự động ghi log warning và trả lỗi HTTP nếu thiếu hoặc sai định dạng.
func GetUserID(c *gin.Context, op string) (uuid.UUID, bool) {
	idStr := strings.TrimSpace(c.GetHeader("X-User-ID"))
	if idStr == "" {
		logger.HandlerWarn(c, op, nil, "unauthorized - missing x-user-id header")
		apires.RespondUnauthorized(c, "unauthorized")
		return uuid.Nil, false
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		logger.HandlerWarn(c, op, err, "invalid x-user-id format")
		apires.RespondBadRequest(c, "invalid request")
		return uuid.Nil, false
	}
	return id, true
}

// [COMMENT]: GetZoneID trích xuất và parse UUID của zone từ header X-Zone-ID.
// Tự động ghi log warning và trả lỗi HTTP nếu thiếu hoặc sai định dạng.
func GetZoneID(c *gin.Context, op string) (uuid.UUID, bool) {
	idStr := strings.TrimSpace(c.GetHeader("X-Zone-ID"))
	if idStr == "" {
		logger.HandlerWarn(c, op, nil, "missing zone context header X-Zone-ID")
		apires.RespondBadRequest(c, "missing zone context")
		return uuid.Nil, false
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		logger.HandlerWarn(c, op, err, "invalid zone id format")
		apires.RespondBadRequest(c, "invalid zone id format")
		return uuid.Nil, false
	}
	return id, true
}

// [COMMENT]: GetTenantID trích xuất và parse UUID của tenant từ header X-Tenant-ID.
// Tự động ghi log warning và trả lỗi HTTP nếu thiếu hoặc sai định dạng.
func GetTenantID(c *gin.Context, op string) (uuid.UUID, bool) {
	idStr := strings.TrimSpace(c.GetHeader("X-Tenant-ID"))
	if idStr == "" {
		logger.HandlerWarn(c, op, nil, "missing tenant context header X-Tenant-ID")
		apires.RespondBadRequest(c, "missing tenant context")
		return uuid.Nil, false
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		logger.HandlerWarn(c, op, err, "invalid tenant id format")
		apires.RespondBadRequest(c, "invalid tenant id format")
		return uuid.Nil, false
	}
	return id, true
}

// [COMMENT]: GetWorkspaceID trích xuất và parse UUID của workspace từ header X-Workspace-ID do ACR inject.
// Tự động ghi log warning và trả lỗi HTTP nếu thiếu hoặc sai định dạng.
func GetWorkspaceID(c *gin.Context, op string) (uuid.UUID, bool) {
	idStr := strings.TrimSpace(c.GetHeader("X-Workspace-ID"))
	if idStr == "" {
		logger.HandlerWarn(c, op, nil, "missing active workspace context header X-Workspace-ID")
		apires.RespondForbidden(c, "missing workspace context")
		return uuid.Nil, false
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		logger.HandlerWarn(c, op, err, "invalid workspace id format")
		apires.RespondForbidden(c, "invalid workspace id format")
		return uuid.Nil, false
	}
	return id, true
}

// [COMMENT]: GetUserName trích xuất username của user từ header X-User-Name do ACR inject.
// Tự động ghi log warning và trả lỗi HTTP nếu thiếu.
func GetUserName(c *gin.Context, op string) (string, bool) {
	username := strings.TrimSpace(c.GetHeader("X-User-Name"))
	if username == "" {
		logger.HandlerWarn(c, op, nil, "missing username context")
		apires.RespondForbidden(c, "missing username context")
		return "", false
	}
	return username, true
}

// [COMMENT]: GetUserRole trích xuất role code của user từ header X-User-Role do ACR inject.
// Tự động ghi log warning và trả lỗi HTTP nếu thiếu.
func GetUserRole(c *gin.Context, op string) (string, bool) {
	role := strings.TrimSpace(c.GetHeader("X-User-Role"))
	if role == "" {
		logger.HandlerWarn(c, op, nil, "missing user role context")
		apires.RespondForbidden(c, "missing user role context")
		return "", false
	}
	return role, true
}

// [COMMENT]: GetUserRoleID trích xuất và parse UUID của role đang hoạt động từ header X-User-Role-ID do ACR inject.
// Tự động ghi log warning và trả lỗi HTTP nếu thiếu hoặc sai định dạng.
func GetUserRoleID(c *gin.Context, op string) (uuid.UUID, bool) {
	idStr := strings.TrimSpace(c.GetHeader("X-User-Role-ID"))
	if idStr == "" {
		logger.HandlerWarn(c, op, nil, "missing active role context header X-User-Role-ID")
		apires.RespondUnauthorized(c, "missing role context")
		return uuid.Nil, false
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		logger.HandlerWarn(c, op, err, "invalid user role id format")
		apires.RespondUnauthorized(c, "missing role context")
		return uuid.Nil, false
	}
	return id, true
}

// [COMMENT]: GetClientDeviceID trích xuất và parse UUID của device đang kết nối từ header X-Client-Device-ID do ACR inject.
// Tự động ghi log warning và trả lỗi HTTP nếu thiếu hoặc sai định dạng.
func GetClientDeviceID(c *gin.Context, op string) (uuid.UUID, bool) {
	idStr := strings.TrimSpace(c.GetHeader("X-Client-Device-ID"))
	if idStr == "" {
		logger.HandlerWarn(c, op, nil, "missing device context header X-Client-Device-ID")
		apires.RespondUnauthorized(c, "unauthorized")
		return uuid.Nil, false
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		logger.HandlerWarn(c, op, err, "invalid client device id format")
		apires.RespondUnauthorized(c, "unauthorized")
		return uuid.Nil, false
	}
	return id, true
}

// [COMMENT]: GetOptionalTenantIDStr trích xuất Tenant ID dưới dạng chuỗi (nếu có, không bắt buộc).
func GetOptionalTenantIDStr(c *gin.Context) string {
	return strings.TrimSpace(c.GetHeader("X-Tenant-ID"))
}

// [COMMENT]: GetOptionalZoneIDStr trích xuất Zone ID dưới dạng chuỗi (nếu có, không bắt buộc).
func GetOptionalZoneIDStr(c *gin.Context) string {
	return strings.TrimSpace(c.GetHeader("X-Zone-ID"))
}

// [COMMENT]: GetTraceparent trích xuất traceparent header cho mục đích tracing telemetry (nếu có).
func GetTraceparent(c *gin.Context) string {
	return strings.TrimSpace(c.GetHeader("traceparent"))
}

// [COMMENT]: GetRequestID trích xuất request ID từ header hoặc trả về rỗng (nếu có).
func GetRequestID(c *gin.Context) string {
	return strings.TrimSpace(c.GetHeader("X-Request-ID"))
}

// [COMMENT]: GetOptionalClientDeviceIDStr trích xuất Device ID dưới dạng chuỗi (nếu có, không bắt buộc).
func GetOptionalClientDeviceIDStr(c *gin.Context) string {
	return strings.TrimSpace(c.GetHeader("X-Client-Device-ID"))
}
