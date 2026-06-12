package middleware

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"controlplane/internal/cacheengine"
	"controlplane/internal/security"
	"controlplane/pkg/apires"
	"controlplane/pkg/constant"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var (
	zoneL1RegistryMu sync.RWMutex
	zoneL1Registry   *cacheengine.CacheRegistry
)

// InitZoneAuth khởi tạo dependencies cho zone authentication middleware.
func InitZoneAuth(registry *cacheengine.CacheRegistry) {
	zoneL1RegistryMu.Lock()
	zoneL1Registry = registry
	zoneL1RegistryMu.Unlock()
}

type zoneIDCtxKey struct{}

// ContextWithZoneID chèn Zone ID vào Go context.
func ContextWithZoneID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, zoneIDCtxKey{}, id)
}

// GetZoneID lấy Zone ID từ Go context.
func GetZoneID(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(zoneIDCtxKey{}).(uuid.UUID)
	return id, ok
}

func extractZoneCode(c *gin.Context) string {
	var code string
	if cookieVal, err := c.Cookie(constant.ZoneCodeName); err == nil {
		code = strings.TrimSpace(cookieVal)
	}
	return code
}

// AdminZoneAuth kiểm tra và so khớp zone định danh cho Admin role.
func AdminZoneAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		const op = "middleware.admin_zone_auth"

		zoneL1RegistryMu.RLock()
		registry := zoneL1Registry
		zoneL1RegistryMu.RUnlock()
		if registry == nil {
			logger.HandlerError(c, op, errors.New("zone l1 registry is not initialized"))
			apires.RespondServiceUnavailable(c, "zone lookup temporarily unavailable")
			c.Abort()
			return
		}

		// Xác minh zone hiện tại của request dựa trên Cookie zone_code
		zoneCode := extractZoneCode(c)
		if zoneCode == "" {
			zoneCode = "global"
		}

		tokenZoneID := strings.TrimSpace(c.GetString(constant.ContextKeyAdminZoneID))

		if strings.EqualFold(zoneCode, "global") {
			// Nếu session token bị ràng buộc vào một zone cụ thể, không được phép truy cập global.
			if tokenZoneID != "" {
				logger.HandlerWarn(c, op, fmt.Errorf("restricted admin token tried to access global"), "admin zone access forbidden")
				apires.RespondForbidden(c, "forbidden: admin is restricted to a specific zone")
				c.Abort()
				return
			}

			// Injects vào Go Context (không inject vào Gin Context)
			ctx := ContextWithZoneID(c.Request.Context(), uuid.Nil)
			c.Request = c.Request.WithContext(ctx)

			c.Next()
			return
		}

		// Với zone code cụ thể, phân giải thông qua L1 Registry
		normalizedZoneCode := strings.ToLower(strings.TrimSpace(zoneCode))
		val, err := registry.GetOrLoad(c.Request.Context(), "zone_by_code", normalizedZoneCode)
		if err != nil {
			logger.HandlerWarn(c, op, err, fmt.Sprintf("failed to resolve zone ID for code: %s", zoneCode))
			apires.RespondBadRequest(c, "invalid zone code")
			c.Abort()
			return
		}

		zoneIDStr, ok := val.(string)
		if !ok || zoneIDStr == "" {
			logger.HandlerWarn(c, op, fmt.Errorf("resolved empty or invalid type zone ID for code: %s", zoneCode), "")
			apires.RespondBadRequest(c, "invalid zone code")
			c.Abort()
			return
		}

		resolvedZoneID, parseErr := uuid.Parse(zoneIDStr)
		if parseErr != nil {
			logger.HandlerError(c, op, parseErr)
			apires.RespondBadRequest(c, "invalid zone configuration")
			c.Abort()
			return
		}

		// Nếu token của Admin có giới hạn zone, so sánh xem có trùng khớp không
		if tokenZoneID != "" {
			if !strings.EqualFold(tokenZoneID, zoneIDStr) {
				logger.HandlerWarn(c, op, fmt.Errorf("admin session zone mismatch: token has %s, request has %s", tokenZoneID, zoneIDStr), "admin zone access forbidden")
				apires.RespondForbidden(c, "forbidden: admin is restricted to a different zone")
				c.Abort()
				return
			}
		}

		// Injects vào Go Context (không inject vào Gin Context)
		ctx := ContextWithZoneID(c.Request.Context(), resolvedZoneID)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}

// UserZoneAuth kiểm tra và so khớp zone định danh cho User role (không chấp nhận global).
func UserZoneAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		const op = "middleware.user_zone_auth"

		zoneL1RegistryMu.RLock()
		registry := zoneL1Registry
		zoneL1RegistryMu.RUnlock()
		if registry == nil {
			logger.HandlerError(c, op, errors.New("zone l1 registry is not initialized"))
			apires.RespondServiceUnavailable(c, "zone lookup temporarily unavailable")
			c.Abort()
			return
		}

		zoneCode := extractZoneCode(c)
		// User bắt buộc phải có zone code hợp lệ và không được phép dùng "global"
		if zoneCode == "" || strings.EqualFold(zoneCode, "global") {
			logger.HandlerWarn(c, op, fmt.Errorf("missing or global zone code rejected for user request"), "")
			apires.RespondBadRequest(c, "valid zone code is required")
			c.Abort()
			return
		}

		// Lấy claims của User đã được lưu ở middleware trước
		valClaims, exists := c.Get(constant.ContextKeyJWTClaims)
		if !exists {
			logger.HandlerError(c, op, errors.New("jwt claims not found in context"))
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}

		claims, ok := valClaims.(security.Claims)
		if !ok {
			logger.HandlerError(c, op, errors.New("failed to cast jwt claims"))
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}

		// Phân giải zone code
		normalizedZoneCode := strings.ToLower(strings.TrimSpace(zoneCode))
		val, err := registry.GetOrLoad(c.Request.Context(), "zone_by_code", normalizedZoneCode)
		if err != nil {
			logger.HandlerWarn(c, op, err, fmt.Sprintf("failed to resolve zone ID for user code: %s", zoneCode))
			apires.RespondBadRequest(c, "invalid zone code")
			c.Abort()
			return
		}

		zoneIDStr, ok := val.(string)
		if !ok || zoneIDStr == "" {
			logger.HandlerWarn(c, op, fmt.Errorf("resolved empty or invalid type zone ID for user code: %s", zoneCode), "")
			apires.RespondBadRequest(c, "invalid zone code")
			c.Abort()
			return
		}

		resolvedZoneID, parseErr := uuid.Parse(zoneIDStr)
		if parseErr != nil {
			logger.HandlerError(c, op, parseErr)
			apires.RespondBadRequest(c, "invalid zone configuration")
			c.Abort()
			return
		}

		// User bắt buộc phải trùng khớp Zone ID
		if claims.ZoneID == "" || !strings.EqualFold(claims.ZoneID, zoneIDStr) {
			logger.HandlerWarn(c, op, fmt.Errorf("user session zone mismatch: token has %s, request has %s", claims.ZoneID, zoneIDStr), "user zone access forbidden")
			apires.RespondForbidden(c, "forbidden: user is restricted to a different zone")
			c.Abort()
			return
		}

		// Injects vào Go Context (không inject vào Gin Context)
		ctx := ContextWithZoneID(c.Request.Context(), resolvedZoneID)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}
