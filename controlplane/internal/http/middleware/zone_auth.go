package middleware

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"controlplane/internal/cacheengine"

	"controlplane/pkg/apires"
	"controlplane/pkg/constant"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var zoneL1Registry atomic.Pointer[cacheengine.CacheRegistry]

// InitZoneAuth khởi tạo cache registry hỗ trợ phân giải nhanh thông tin Zone.
func InitZoneAuth(registry *cacheengine.CacheRegistry) {
	zoneL1Registry.Store(registry)
}

// ContextWithZoneID chèn Zone ID đã xác thực vào Go context để tầng Service sử dụng.
// Sử dụng constant.ZoneIDCtxKey để đảm bảo các tầng khác có thể tự trích xuất mà không bị coupling.
func ContextWithZoneID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, constant.ZoneIDCtxKey, id)
}

// GetZoneID trích xuất Zone ID đã xác thực từ Go context.
func GetZoneID(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(constant.ZoneIDCtxKey).(uuid.UUID)
	return id, ok
}

// extractZoneCode lấy mã định danh zone (zone code) từ Cookie của request.
func extractZoneCode(c *gin.Context) string {
	var code string
	if cookieVal, err := c.Cookie(constant.ZoneCodeName); err == nil {
		code = strings.TrimSpace(cookieVal)
	}
	return code
}

// ZoneAuth là middleware kiểm soát ranh giới Zone độc lập với vai trò (identity-agnostic).
// Quyết định cho phép truy cập dựa trên cấu hình yêu cầu của Route (allowGlobal)
// kết hợp đối chiếu với các ràng buộc bảo mật (claims/constraints) của phiên làm việc.
func ZoneAuth(allowGlobal bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		const op = "middleware.zone_auth"

		// Đọc cache registry an toàn đa luồng không dùng lock (lock-free)
		registry := zoneL1Registry.Load()
		if registry == nil {
			logger.HandlerError(c, op, errors.New("zone l1 registry is not initialized"))
			apires.RespondServiceUnavailable(c, "zone lookup temporarily unavailable")
			c.Abort()
			return
		}

		// 1. Xác định mã zone từ request
		zoneCode := extractZoneCode(c)
		if zoneCode == "" {
			zoneCode = "global"
		}

		// 2. Trích xuất vai trò và ràng buộc bảo mật từ session (Admin vs User)
		var sessionZoneID string
		var hasSession bool
		var isAdmin bool

		if ident, ok := c.Request.Context().Value(constant.IdentityKey).(*constant.Identity); ok && ident != nil {
			hasSession = true
			if ident.UserID == "sre" {
				isAdmin = true
				sessionZoneID = strings.TrimSpace(ident.ZoneID)
			} else {
				sessionZoneID = strings.TrimSpace(ident.ZoneID)

				// User session thông thường bắt buộc phải được gán vào 1 Zone cụ thể (không được để trống)
				if sessionZoneID == "" {
					logger.HandlerWarn(c, op, errors.New("user claims missing zone ID constraint"), "user zone access forbidden")
					apires.RespondForbidden(c, "forbidden: user session requires a valid zone constraint")
					c.Abort()
					return
				}
			}
		}

		// Bảo mật an toàn: Nếu không tìm thấy thông tin xác thực nào từ các bước trước, từ chối truy cập
		if !hasSession {
			logger.HandlerError(c, op, errors.New("unauthenticated: missing security context for zone verification"))
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}

		// 3. Xử lý kịch bản truy cập Global
		if strings.EqualFold(zoneCode, "global") {
			// Nếu Endpoint này là Write Path hoặc yêu cầu có Zone cụ thể (allowGlobal = false) -> Từ chối
			if !allowGlobal {
				logger.HandlerWarn(c, op, fmt.Errorf("global access rejected for this endpoint"), "")
				apires.RespondBadRequest(c, "valid zone code is required (global not allowed)")
				c.Abort()
				return
			}

			// Nếu Endpoint cho phép global:
			// - Đối với User: Nếu bị giới hạn ở một zone nhất định -> Từ chối truy cập global
			// - Đối với Admin: Được phép truy cập global bất kể token có mang zoneID hay không (để xem/quản trị toàn cục)
			if !isAdmin && sessionZoneID != "" {
				logger.HandlerWarn(c, op, fmt.Errorf("restricted user session tried to access global: session_zone=%s", sessionZoneID), "zone access forbidden")
				apires.RespondForbidden(c, "forbidden: session is restricted to a specific zone")
				c.Abort()
				return
			}

			// Hợp lệ: Ghi nhận global zone ID (uuid.Nil) vào Go context
			ctx := ContextWithZoneID(c.Request.Context(), uuid.Nil)
			c.Request = c.Request.WithContext(ctx)
			c.Next()
			return
		}

		// 4. Xử lý kịch bản truy cập Zone cụ thể
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
			logger.HandlerWarn(c, op, fmt.Errorf("resolved empty zone ID for code: %s", zoneCode), "")
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

		// Kiểm tra ràng buộc ranh giới zone (áp dụng cho cả Admin lẫn User nếu có ràng buộc cụ thể và không phải global)
		if sessionZoneID != "" && !strings.EqualFold(sessionZoneID, "global") && sessionZoneID != uuid.Nil.String() {
			if !strings.EqualFold(sessionZoneID, zoneIDStr) {
				logger.HandlerWarn(c, op, fmt.Errorf("session zone mismatch: session has %s, request has %s", sessionZoneID, zoneIDStr), "zone access forbidden")
				apires.RespondForbidden(c, "forbidden: session is restricted to a different zone")
				c.Abort()
				return
			}
		}

		// Kiểm tra trạng thái hoạt động của Zone nếu là User thường (Admin được phép bypass hoạt động check status này)
		if !isAdmin {
			valStatus, err := registry.GetOrLoad(c.Request.Context(), "zone_status_by_id", resolvedZoneID.String())
			if err != nil {
				logger.HandlerWarn(c, op, err, "failed to resolve zone status")
				apires.RespondForbidden(c, "forbidden: zone status verification failed")
				c.Abort()
				return
			}

			zoneStatus, ok := valStatus.(string)
			if !ok || zoneStatus != "active" {
				logger.HandlerWarn(c, op, fmt.Errorf("user tried to access non-active zone: status=%v", valStatus), "")
				apires.RespondForbidden(c, "forbidden: zone is currently not active")
				c.Abort()
				return
			}
		}

		// Hợp lệ: Ghi nhận UUID của Zone đã xác thực vào Go context
		ctx := ContextWithZoneID(c.Request.Context(), resolvedZoneID)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// ZoneRequired yêu cầu client cung cấp một zone code cụ thể và từ chối truy cập global.
func ZoneRequired() gin.HandlerFunc {
	return ZoneAuth(false)
}

// ZoneOptional cho phép client truy cập qua một zone code cụ thể hoặc global.
func ZoneOptional() gin.HandlerFunc {
	return ZoneAuth(true)
}
