package middleware

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"controlplane/internal/security"
	apires "controlplane/pkg/apires"
	"controlplane/pkg/constant"

	"github.com/gin-gonic/gin"
)

const adminAPIKeyRotationTriggerTTL = 10 * time.Minute

// adminAPIKeyAuthState giữ dependency runtime cho admin API-key middleware.
//
// Source of truth:
// - app/module.go gọi InitAdminAPIKeyAuth đúng một lần khi dựng module graph.
// - Route chỉ gọi AdminAPIKeyAuth(...) và không truyền dependency của IAM vào.
//
// Boundary:
//   - Middleware không import IAM/Core module.
//   - Middleware chỉ biết các contract tối thiểu: secret provider, verify device
//     secret, và marker yêu cầu rotation khi token hết hạn.
var adminAPIKeyAuthState = struct {
	mu                  sync.RWMutex
	secrets             security.SecretProvider
	verifyDeviceSecret  func(ctx context.Context, deviceID string, deviceSecret string) (bool, error)
	setRotationRequired func(ctx context.Context, ttl time.Duration) error
}{}

type adminAuthOptions struct {
	injectDeviceID     bool
	injectDeviceSecret bool
	injectTokenJTI     bool
}

// AdminAuthOption chỉ điều khiển dữ liệu nào được inject vào gin.Context
// sau khi admin runtime auth đã pass.
//
// Ví dụ:
// - route logout cần device_id để service revoke đúng runtime device.
// - critical action cần device_id để signature guard build canonical payload.
type AdminAuthOption func(*adminAuthOptions)

// InitAdminAPIKeyAuth khởi tạo dependency cho AdminAPIKeyAuth.
//
// Hàm này thuộc tầng global wiring, không gọi trong route và không gọi trong
// từng module handler. Nếu thiếu dependency, middleware sẽ fail-closed với 503.
func InitAdminAPIKeyAuth(
	sp security.SecretProvider,
	verifyDeviceSecret func(ctx context.Context, deviceID string, deviceSecret string) (bool, error),
	setRotationRequired func(ctx context.Context, ttl time.Duration) error,
) error {
	if sp == nil {
		return errors.New("admin api key auth: secret provider is required")
	}
	if verifyDeviceSecret == nil {
		return errors.New("admin api key auth: verify device secret is required")
	}
	if setRotationRequired == nil {
		return errors.New("admin api key auth: rotation trigger is required")
	}

	adminAPIKeyAuthState.mu.Lock()
	adminAPIKeyAuthState.secrets = sp
	adminAPIKeyAuthState.verifyDeviceSecret = verifyDeviceSecret
	adminAPIKeyAuthState.setRotationRequired = setRotationRequired
	adminAPIKeyAuthState.mu.Unlock()
	return nil
}

func WithInjectDeviceID() AdminAuthOption {
	return func(options *adminAuthOptions) {
		if options != nil {
			options.injectDeviceID = true
		}
	}
}

func WithInjectDeviceSecret() AdminAuthOption {
	return func(options *adminAuthOptions) {
		if options != nil {
			options.injectDeviceSecret = true
		}
	}
}

func WithInjectTokenJTI() AdminAuthOption {
	return func(options *adminAuthOptions) {
		if options != nil {
			options.injectTokenJTI = true
		}
	}
}

// AdminAPIKeyAuth xác thực admin runtime session bằng 3 cookie:
// - admin_api_token: JWT ký bởi secret family admin_api_key.
// - device_id: device binding ID claim trong JWT phải khớp cookie này.
// - device_secret: secret fragment kiểm tra qua runtime cache.
//
// Thứ tự guard cố ý rõ ràng:
// 1. Đọc đủ 3 cookie.
// 2. Load dependency đã init ở app/module.go.
// 3. Parse JWT bằng tất cả secret candidates để hỗ trợ rotation.
// 4. Nếu token expired, đánh dấu cần rotation nhưng vẫn trả unauthorized.
// 5. Check device_id claim khớp cookie.
// 6. Verify device_secret qua runtime cache.
// 7. Inject context theo option của route rồi mới cho request đi tiếp.
func AdminAPIKeyAuth(opts ...AdminAuthOption) gin.HandlerFunc {
	options := adminAuthOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	return func(c *gin.Context) {
		// 1) Admin runtime auth bắt buộc có đủ 3 cookie fragment.
		// Thiếu bất kỳ phần nào đều trả generic 401 để không leak trạng thái.
		token, ok := readAdminCookie(c, constant.AdminAPITokenName)
		if !ok {
			return
		}
		deviceID, ok := readAdminCookie(c, constant.DeviceIDName)
		if !ok {
			return
		}
		deviceSecret, ok := readAdminCookie(c, constant.DeviceSecretName)
		if !ok {
			return
		}

		// 2) Dependency phải được init từ app/module.go trước khi register route.
		// Nếu chưa init, đây là lỗi runtime wiring nên trả 503 thay vì 401.
		adminAPIKeyAuthState.mu.RLock()
		secrets := adminAPIKeyAuthState.secrets
		verifyDeviceSecret := adminAPIKeyAuthState.verifyDeviceSecret
		setRotationRequired := adminAPIKeyAuthState.setRotationRequired
		adminAPIKeyAuthState.mu.RUnlock()
		if secrets == nil || verifyDeviceSecret == nil || setRotationRequired == nil {
			abortAdminAuthUnavailable(c)
			return
		}

		// 3) Parse token bằng candidate list để hỗ trợ secret rotation.
		// ErrTokenExpired được ghi nhận riêng để kích hoạt rotation marker.
		candidates, err := secrets.GetCandidates(c.Request.Context(), security.SecretFamilyAdminAPIKey)
		if err != nil || len(candidates) == 0 {
			abortAdminAuthUnavailable(c)
			return
		}

		var claims security.Claims
		parsed := false
		expired := false
		for _, candidate := range candidates {
			parsedClaims, parseErr := security.Parse(token, candidate.Value)
			if parseErr == nil {
				claims = parsedClaims
				parsed = true
				break
			}
			if errors.Is(parseErr, security.ErrTokenExpired) {
				expired = true
			}
			if errors.Is(parseErr, security.ErrEmptySecret) {
				abortAdminAuthUnavailable(c)
				return
			}
		}
		if !parsed {
			abortAdminUnauthorized(c)
			return
		}
		if expired {
			// Không block trên marker lỗi: request này vẫn bị từ chối phía dưới,
			// còn background rotation có thể retry ở request sau.
			_ = setRotationRequired(c.Request.Context(), adminAPIKeyRotationTriggerTTL)
		}

		// 4) Token phải bind với đúng device_id cookie. Đây là lớp chống copy
		// riêng admin_api_token sang một device runtime khác.
		if strings.TrimSpace(claims.DeviceID) == "" || claims.DeviceID != deviceID {
			abortAdminUnauthorized(c)
			return
		}

		// 5) device_secret là fragment runtime trong Redis. Token đúng nhưng
		// device_secret sai hoặc không còn tồn tại thì session không hợp lệ.
		verified, err := verifyDeviceSecret(c.Request.Context(), deviceID, deviceSecret)
		if err != nil {
			abortAdminAuthUnavailable(c)
			return
		}
		if !verified {
			abortAdminUnauthorized(c)
			return
		}

		// 6) Chỉ inject dữ liệu mà route cần. Route thường chỉ cần auth pass,
		// critical route cần device_id để middleware signature chạy phía sau.
		if options.injectDeviceID {
			c.Set(constant.ContextKeyAdminDeviceID, deviceID)
		}
		if options.injectDeviceSecret {
			c.Set(constant.ContextKeyAdminDeviceSecret, deviceSecret)
		}
		if options.injectTokenJTI {
			c.Set(constant.ContextKeyAdminTokenJTI, strings.TrimSpace(claims.TokenID))
		}
		c.Next()
	}
}

// readAdminCookie gom rule "cookie bắt buộc + trim + generic unauthorized".
// Không trả tên cookie bị thiếu để tránh user enumeration/runtime probing.
func readAdminCookie(c *gin.Context, name string) (string, bool) {
	value, err := c.Cookie(name)
	if err != nil || strings.TrimSpace(value) == "" {
		abortAdminUnauthorized(c)
		return "", false
	}
	return strings.TrimSpace(value), true
}

func abortAdminUnauthorized(c *gin.Context) {
	apires.RespondUnauthorized(c, "unauthorized")
	c.Abort()
}

func abortAdminAuthUnavailable(c *gin.Context) {
	apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
	c.Abort()
}
