package pkgcontext

import (
	"strings"

	"cost-manager/api/pkg/apires"
	"cost-manager/api/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// [COMMENT]: Định nghĩa các hằng số key chuẩn để lưu trữ dữ liệu định danh trong Gin Context.
// Giúp tránh gõ nhầm chuỗi tĩnh và đồng bộ giữa Middleware Injector, Middleware Auth và HTTP Handlers.
const (
	CtxUserID   = "ctx_user_id"
	CtxZoneID   = "ctx_zone_id"
	CtxTenantID = "ctx_tenant_id"
	CtxUserName = "ctx_username"
)

// [COMMENT]: Identity là struct đại diện cho bộ thông tin định danh tin cậy (trusted identity) được truyền từ Edge Gateway.
type Identity struct {
	UserID   uuid.UUID
	Username string
	ZoneID   uuid.UUID
	TenantID string
}

// [COMMENT]: GetUserID trích xuất và validate User UUID từ Gin Context.
// Nếu thiếu hoặc sai định dạng, tự động log warning và gửi HTTP response Unauthorized (401).
func GetUserID(c *gin.Context, op string) (uuid.UUID, bool) {
	val, ok := c.Get(CtxUserID)
	if !ok {
		// [COMMENT]: Log cảnh báo khi context thiếu thông tin x-user-id
		logger.HandlerWarn(c, op, nil, "unauthorized - missing user id context")
		apires.RespondUnauthorized(c, "trusted billing identity is missing or invalid")
		return uuid.Nil, false
	}
	if err, isErr := val.(error); isErr {
		// [COMMENT]: Log cảnh báo khi chuỗi UUID của user-id không đúng format
		logger.HandlerWarn(c, op, err, "invalid x-user-id format in context")
		apires.RespondUnauthorized(c, "trusted billing identity is missing or invalid")
		return uuid.Nil, false
	}
	if id, isUUID := val.(uuid.UUID); isUUID && id != uuid.Nil {
		return id, true
	}
	logger.HandlerWarn(c, op, nil, "invalid user id context type")
	apires.RespondUnauthorized(c, "trusted billing identity is missing or invalid")
	return uuid.Nil, false
}

// [COMMENT]: GetZoneID trích xuất và validate Zone UUID từ Gin Context.
// Nếu thiếu hoặc sai định dạng, tự động log warning và gửi HTTP response Unauthorized (401).
func GetZoneID(c *gin.Context, op string) (uuid.UUID, bool) {
	val, ok := c.Get(CtxZoneID)
	if !ok {
		// [COMMENT]: Log cảnh báo khi context thiếu thông tin x-zone-id
		logger.HandlerWarn(c, op, nil, "unauthorized - missing zone id context")
		apires.RespondUnauthorized(c, "trusted billing identity is missing or invalid")
		return uuid.Nil, false
	}
	if err, isErr := val.(error); isErr {
		// [COMMENT]: Log cảnh báo khi chuỗi UUID của zone-id không đúng format
		logger.HandlerWarn(c, op, err, "invalid x-zone-id format in context")
		apires.RespondUnauthorized(c, "trusted billing identity is missing or invalid")
		return uuid.Nil, false
	}
	if id, isUUID := val.(uuid.UUID); isUUID && id != uuid.Nil {
		return id, true
	}
	logger.HandlerWarn(c, op, nil, "invalid zone id context type")
	apires.RespondUnauthorized(c, "trusted billing identity is missing or invalid")
	return uuid.Nil, false
}

// [COMMENT]: GetUserName trích xuất Username từ Gin Context.
// Nếu thiếu hoặc vượt quá độ dài quy định, tự động log warning và gửi HTTP response Unauthorized (401).
func GetUserName(c *gin.Context, op string) (string, bool) {
	val, ok := c.Get(CtxUserName)
	if !ok {
		// [COMMENT]: Log cảnh báo khi context thiếu x-user-name
		logger.HandlerWarn(c, op, nil, "unauthorized - missing username context")
		apires.RespondUnauthorized(c, "trusted billing identity is missing or invalid")
		return "", false
	}
	if username, isStr := val.(string); isStr {
		trimmed := strings.TrimSpace(username)
		if trimmed != "" && len(trimmed) <= 128 {
			return trimmed, true
		}
	}
	logger.HandlerWarn(c, op, nil, "invalid or oversized username context")
	apires.RespondUnauthorized(c, "trusted billing identity is missing or invalid")
	return "", false
}

// [COMMENT]: GetTenantID trích xuất TenantID (tùy chọn) từ Gin Context.
// Không bắt buộc phải có, nhưng nếu có thì không được vượt quá 128 ký tự. Trả về string rỗng nếu không có.
func GetTenantID(c *gin.Context, op string) string {
	val, ok := c.Get(CtxTenantID)
	if !ok {
		return ""
	}
	if tenantID, isStr := val.(string); isStr {
		trimmed := strings.TrimSpace(tenantID)
		if len(trimmed) <= 128 {
			return trimmed
		}
		logger.HandlerWarn(c, op, nil, "oversized tenant id header ignored")
	}
	return ""
}

// [COMMENT]: GetIdentity gom toàn bộ bộ định danh đầy đủ từ Gin Context thành struct Identity.
// Trả về false nếu bất kỳ trường thông tin bắt buộc nào (UserID, ZoneID, Username) bị thiếu/không hợp lệ.
func GetIdentity(c *gin.Context, op string) (Identity, bool) {
	userID, userOk := GetUserID(c, op)
	if !userOk {
		return Identity{}, false
	}
	zoneID, zoneOk := GetZoneID(c, op)
	if !zoneOk {
		return Identity{}, false
	}
	username, nameOk := GetUserName(c, op)
	if !nameOk {
		return Identity{}, false
	}
	tenantID := GetTenantID(c, op)

	return Identity{
		UserID:   userID,
		Username: username,
		ZoneID:   zoneID,
		TenantID: tenantID,
	}, true
}
