// ============================================================================
// 🛡️ ARCHITECTURAL COMPONENT: IDENTITY EXTRACTION MIDDLEWARE (HEADER-BASED)
// ============================================================================
//
// 🤝 1. SYSTEM CONTRACT:
//   - Đọc thông tin định danh (Identity) đã được gRPC ACL Service xác thực 100%
//     từ API Gateway / Envoy biên gửi xuống thông qua các HTTP Headers.
//   - Tiêm (Inject) thực thể `Identity` vào Go standard context cho các handler xử lý.
//   - Ngăn chặn triệt để (Fail-Closed) mọi request bypass Gateway (thiếu x-user-id).
//
// 💡 2. OPTION PATTERN:
//   - Hỗ trợ các options (WithInjectAccessKey, WithInjectAccessSecret, v.v.) để
//     tương thích ngược hoàn toàn với cấu hình route hiện tại mà không làm phình context.
//
// ============================================================================

package middleware

import (
	"context"
	"strconv"
	"strings"

	"controlplane/pkg/apires"
	"controlplane/pkg/constant"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// [COMMENT]: ContextWithZoneID chèn Zone ID đã xác thực vào Go context để tầng Service sử dụng.
func ContextWithZoneID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, constant.ZoneIDCtxKey, id)
}

// [COMMENT]: GetZoneID trích xuất Zone ID đã xác thực từ Go context.
func GetZoneID(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(constant.ZoneIDCtxKey).(uuid.UUID)
	return id, ok
}

type aclOptions struct {
	injectAccessKey     bool
	injectAccessSecret  bool
	injectTrackedDevice bool
}

// ACLOption định nghĩa hàm cấu hình tùy chọn cho Identity extraction.
type ACLOption func(*aclOptions)

// WithInjectAccessKey kích hoạt tiêm AccessKey từ header x-access-key.
func WithInjectAccessKey() ACLOption {
	return func(o *aclOptions) {
		o.injectAccessKey = true
	}
}

// WithInjectAccessSecret kích hoạt tiêm AccessSecret từ header x-access-secret.
func WithInjectAccessSecret() ACLOption {
	return func(o *aclOptions) {
		o.injectAccessSecret = true
	}
}

// WithInjectTrackedDevice kích hoạt tiêm TrackedDeviceID từ header x-device-id.
func WithInjectTrackedDevice() ACLOption {
	return func(o *aclOptions) {
		o.injectTrackedDevice = true
	}
}

// ACL trả về gin.HandlerFunc trích xuất thông tin định danh từ Envoy headers.
// Thay thế hoàn toàn cơ chế tự kiểm tra JWT/Redis/Vault cũ trên Control Plane.
func ACL(opts ...ACLOption) gin.HandlerFunc {
	options := &aclOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(options)
		}
	}

	return func(c *gin.Context) {
		// 1. Trích xuất UserID từ Header (bắt buộc phải có để chống bypass)
		userID := strings.TrimSpace(c.GetHeader("x-user-id"))
		if userID == "" {
			apires.RespondUnauthorized(c, "unauthorized - gateway validation required")
			c.Abort()
			return
		}

		// 2. Trích xuất các trường định danh cơ bản
		role := strings.TrimSpace(c.GetHeader("x-role"))
		tenantID := strings.TrimSpace(c.GetHeader("x-tenant-id"))
		zoneID := strings.TrimSpace(c.GetHeader("x-zone-id"))

		levelStr := strings.TrimSpace(c.GetHeader("x-level"))
		level := 0
		if levelStr != "" {
			if l, err := strconv.Atoi(levelStr); err == nil {
				level = l
			}
		}

		// 3. Khởi tạo thực thể Identity
		ident := &constant.Identity{
			UserID:   userID,
			Role:     role,
			TenantID: tenantID,
			Level:    level,
			ZoneID:   zoneID,
		}

		// 4. Inject các trường tùy chọn theo cấu hình của Router
		if options.injectAccessKey {
			ident.AccessKey = strings.TrimSpace(c.GetHeader("x-access-key"))
		}
		if options.injectAccessSecret {
			ident.AccessSecret = strings.TrimSpace(c.GetHeader("x-access-secret"))
		}
		if options.injectTrackedDevice {
			ident.TrackedDeviceID = strings.TrimSpace(c.GetHeader("x-device-id"))
		}

		// 5. [COMMENT]: Lưu trữ Identity vào Go standard context
		goCtx := context.WithValue(c.Request.Context(), constant.IdentityKey, ident)
		if zoneID != "" {
			if parsedUUID, err := uuid.Parse(zoneID); err == nil {
				goCtx = context.WithValue(goCtx, constant.ZoneIDCtxKey, parsedUUID)
			}
		}
		c.Request = c.Request.WithContext(goCtx)

		c.Next()
	}
}
