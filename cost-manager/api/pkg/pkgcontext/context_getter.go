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

// [COMMENT]: GetTenantID trích xuất và validate Tenant UUID từ Gin Context (fail-closed).
// Nếu thiếu hoặc sai định dạng context, tự động log warning và gửi HTTP response (403 Forbidden / 401 Unauthorized).
func GetTenantID(c *gin.Context, op string) (uuid.UUID, bool) {
	val, ok := c.Get(CtxTenantID)
	if !ok {
		// [COMMENT]: Log cảnh báo và phản hồi 403 Forbidden khi thiếu tenant context
		logger.HandlerWarn(c, op, nil, "missing tenant identity context")
		apires.RespondForbidden(c, "verified tenant context is required")
		return uuid.Nil, false
	}
	if err, invalid := val.(error); invalid {
		// [COMMENT]: Log cảnh báo và phản hồi 401 Unauthorized khi tenant context sai định dạng
		logger.HandlerWarn(c, op, err, "invalid tenant identity context")
		apires.RespondUnauthorized(c, "trusted billing identity is missing or invalid")
		return uuid.Nil, false
	}
	if tenantID, isUUID := val.(uuid.UUID); isUUID && tenantID != uuid.Nil {
		return tenantID, true
	}
	if tenantStr, isStr := val.(string); isStr {
		if id, err := uuid.Parse(strings.TrimSpace(tenantStr)); err == nil && id != uuid.Nil {
			return id, true
		}
	}
	// [COMMENT]: Log cảnh báo và phản hồi 403 Forbidden khi tenant identity không hợp lệ
	logger.HandlerWarn(c, op, nil, "non-concrete tenant identity context")
	apires.RespondForbidden(c, "verified tenant context is required")
	return uuid.Nil, false
}
