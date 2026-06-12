// ============================================================================
// 🛡️ ARCHITECTURAL & SYSTEM CONTRACTS
// ============================================================================
//
// 🤝 1. SYSTEM CONTRACT
//   - Accepted Cookies: Yêu cầu bắt buộc 3 cookie (access_token, access_key,
//     access_secret). Trả về generic 401 nếu thiếu để chống user enumeration.
//   - Token Validation: Phân tích token bằng danh sách candidates hỗ trợ secret rotation.
//   - Fail-Closed: Nếu thiếu callback khởi tạo hoặc các cấu hình bắt buộc, middleware
//     sẽ tự động từ chối request với mã lỗi 503 Service Unavailable để tự bảo vệ.
//
// 📖 2. SOURCE OF TRUTH
//   - Callback và dependency được app/module.go đọc từ cấu hình và truyền vào
//     InitAdminAPIKeyAuth duy nhất một lần trong quá trình bootstrap hệ thống.
//   - Router chỉ gọi middleware.AdminAPIKeyAuth() mà không cần truyền lại tham số.
//
// 🚧 3. SYSTEM BOUNDARY
//   - Middleware decoupled hoàn toàn, không import trực tiếp các module IAM/Core.
//   - Chỉ giao tiếp thông qua các contract callback được tiêm vào (verifyAccessSecret,
//     setRotationRequired).
//
// 💡 4. OPERATIONAL NOTES
//   - Hiệu năng: Xác thực token và so khớp cookie trực tiếp trên RAM giúp giảm thiểu
//     độ trễ tối đa. Chỉ gọi DB/Redis thông qua callback verifyAccessSecret khi token hợp lệ.

package middleware

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"controlplane/internal/cacheengine"
	coreEntity "controlplane/internal/core/domain/entity"
	"controlplane/internal/security"
	apires "controlplane/pkg/apires"
	"controlplane/pkg/constant"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
)

// adminAPIKeyRotationTriggerTTL là TTL (khóa chặn) của cờ yêu cầu xoay vòng khóa.
// Khi token hết hạn, cờ này được ghi nhận ngay lập tức để kích hoạt background worker xoay khóa tức thời.
// Cờ có TTL 10 phút đóng vai trò rate-limiter, ngăn chặn việc hàng loạt request hết hạn song song cùng
// gọi lệnh kích hoạt xoay khóa liên tiếp gây quá tải hệ thống (stampede prevention).
const adminAPIKeyRotationTriggerTTL = 10 * time.Minute

// adminAPIKeyAuthState giữ dependency runtime cho admin API-key middleware.
var adminAPIKeyAuthState = struct {
	mu                  sync.RWMutex
	registry            *cacheengine.CacheRegistry
	verifyAccessSecret  func(ctx context.Context, accessKey string, accessSecret string) (bool, error)
	setRotationRequired func(ctx context.Context, ttl time.Duration) error
}{}

type adminAuthOptions struct {
	injectAccessKey    bool
	injectAccessSecret bool
	injectTokenJTI     bool
	injectZoneID       bool
}

// AdminAuthOption chỉ điều khiển dữ liệu nào được inject vào gin.Context
// sau khi admin runtime auth đã pass.
type AdminAuthOption func(*adminAuthOptions)

// InitAdminAPIKeyAuth khởi tạo dependency cho AdminAPIKeyAuth.
func InitAdminAPIKeyAuth(
	registry *cacheengine.CacheRegistry,
	verifyAccessSecret func(ctx context.Context, accessKey string, accessSecret string) (bool, error),
	setRotationRequired func(ctx context.Context, ttl time.Duration) error,
) error {
	if registry == nil {
		return errors.New("admin api key auth: cache registry is required")
	}
	if verifyAccessSecret == nil {
		return errors.New("admin api key auth: verify access secret is required")
	}
	if setRotationRequired == nil {
		return errors.New("admin api key auth: rotation trigger is required")
	}

	adminAPIKeyAuthState.mu.Lock()
	adminAPIKeyAuthState.registry = registry
	adminAPIKeyAuthState.verifyAccessSecret = verifyAccessSecret
	adminAPIKeyAuthState.setRotationRequired = setRotationRequired
	adminAPIKeyAuthState.mu.Unlock()
	return nil
}

// WithInjectAccessKey chỉ định middleware tiêm access_key của Admin vào Gin Context.
// Cần thiết cho các middleware bảo mật chạy phía sau (như kiểm tra signature).
func WithInjectAccessKey() AdminAuthOption {
	return func(options *adminAuthOptions) {
		if options != nil {
			options.injectAccessKey = true
		}
	}
}

// WithInjectAccessSecret chỉ định middleware tiêm access_secret vào Gin Context.
// Thường dùng cho các luồng nghiệp vụ đặc thù cần so khớp session hoặc revoke.
func WithInjectAccessSecret() AdminAuthOption {
	return func(options *adminAuthOptions) {
		if options != nil {
			options.injectAccessSecret = true
		}
	}
}

// WithInjectTokenJTI chỉ định middleware tiêm JTI (JWT ID) của token vào Gin Context.
// Phục vụ việc định danh và kiểm tra trạng thái thu hồi của chính tấm token này.
func WithInjectTokenJTI() AdminAuthOption {
	return func(options *adminAuthOptions) {
		if options != nil {
			options.injectTokenJTI = true
		}
	}
}

// WithInjectZoneID chỉ định middleware tiêm ZoneID của Admin vào Gin Context.
func WithInjectZoneID() AdminAuthOption {
	return func(options *adminAuthOptions) {
		if options != nil {
			options.injectZoneID = true
		}
	}
}

// AdminAPIKeyAuth xác thực admin runtime session bằng 3 cookie.
func AdminAPIKeyAuth(opts ...AdminAuthOption) gin.HandlerFunc {
	options := adminAuthOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	return func(c *gin.Context) {
		// Xác định xem đây có phải là yêu cầu đăng xuất hay không
		isLogout := c.Request.URL.Path == "/admin/auth/logout"

		// Đọc giá trị 3 cookie bảo mật của Admin
		tokenCookie, errToken := c.Cookie(constant.AdminAPITokenName)
		accessKeyCookie, errKey := c.Cookie(constant.AccessKeyName)
		accessSecretCookie, errSecret := c.Cookie(constant.AccessSecretName)

		token := strings.TrimSpace(tokenCookie)
		accessKey := strings.TrimSpace(accessKeyCookie)
		accessSecret := strings.TrimSpace(accessSecretCookie)

		// Nếu thiếu cookie và đây không phải luồng logout -> Trả lỗi 401
		if errToken != nil || errKey != nil || errSecret != nil || token == "" || accessKey == "" || accessSecret == "" {
			if isLogout {
				// Nếu là đăng xuất, cho phép đi tiếp để Handler xoá sạch cookie phía Client
				c.Next()
				return
			}
			logger.HandlerWarn(c, "admin.auth.cookie", nil, "missing or empty admin cookie")
			abortAdminUnauthorized(c)
			return
		}

		// --------------------------------------------------------------------
		// 🔄 Load các dependency đã được khởi tạo lúc Bootstrap.
		//   Nếu chưa init, trả lỗi 503 Service Unavailable để tự bảo vệ (Fail-Closed).
		// --------------------------------------------------------------------
		adminAPIKeyAuthState.mu.RLock()
		registry := adminAPIKeyAuthState.registry
		verifyAccessSecret := adminAPIKeyAuthState.verifyAccessSecret
		setRotationRequired := adminAPIKeyAuthState.setRotationRequired
		adminAPIKeyAuthState.mu.RUnlock()
		if registry == nil || verifyAccessSecret == nil || setRotationRequired == nil {
			if isLogout {
				c.Next()
				return
			}
			abortAdminAuthUnavailable(c)
			return
		}

		// --------------------------------------------------------------------
		// 🔄 Parse JWT bằng danh sách các candidates để hỗ trợ rotation.
		// --------------------------------------------------------------------
		val, err := registry.GetOrLoad(c.Request.Context(), "admin_api_key", "")
		if err != nil {
			if isLogout {
				c.Next()
				return
			}
			abortAdminAuthUnavailable(c)
			return
		}
		secrets, ok := val.(*coreEntity.RuntimeSecrets)
		if !ok || secrets == nil {
			if isLogout {
				c.Next()
				return
			}
			abortAdminAuthUnavailable(c)
			return
		}

		candidates := []coreEntity.RuntimeSecret{secrets.Active, secrets.Standby}
		var claims security.Claims
		parsed := false
		expired := false
		for _, candidate := range candidates {
			parsedClaims, parseErr := security.Parse(token, candidate.Secret)
			if parseErr == nil {
				claims = parsedClaims
				parsed = true
				break
			}
			if errors.Is(parseErr, security.ErrTokenExpired) {
				expired = true
			}
			if errors.Is(parseErr, security.ErrEmptySecret) {
				if isLogout {
					c.Next()
					return
				}
				abortAdminAuthUnavailable(c)
				return
			}
		}

		// --------------------------------------------------------------------
		// 🔄 Nếu phát hiện token đã hết hạn, kích hoạt background rotation marker
		//   để hệ thống xoay khóa trong nền. Request hiện tại vẫn bị từ chối (401).
		// --------------------------------------------------------------------
		if expired {
			_ = setRotationRequired(c.Request.Context(), adminAPIKeyRotationTriggerTTL)
		}

		if !parsed {
			if isLogout {
				c.Next()
				return
			}
			abortAdminUnauthorized(c)
			return
		}

		// --------------------------------------------------------------------
		// 🔄 Kiểm tra access_key claim khớp cookie gửi lên.
		//   Giúp chống copy trộm admin_api_token sang thiết bị khác sử dụng.
		// --------------------------------------------------------------------
		if strings.TrimSpace(claims.AccessKey) == "" || claims.AccessKey != accessKey {
			if isLogout {
				c.Next()
				return
			}
			abortAdminUnauthorized(c)
			return
		}

		// --------------------------------------------------------------------
		// 🔄 Verify access_secret qua runtime cache (Redis).
		// --------------------------------------------------------------------
		verified, err := verifyAccessSecret(c.Request.Context(), accessKey, accessSecret)
		if err != nil {
			if isLogout {
				c.Next()
				return
			}
			abortAdminAuthUnavailable(c)
			return
		}
		if !verified {
			if isLogout {
				c.Next()
				return
			}
			abortAdminUnauthorized(c)
			return
		}

		// --------------------------------------------------------------------
		// 🔄 Inject các thông tin cần thiết vào context dựa trên option.
		// --------------------------------------------------------------------
		// [ignoring loop detection]
		goCtx := c.Request.Context()
		c.Set(constant.ContextKeyUserID, claims.Subject)
		c.Set(constant.ContextKeyLevel, claims.Level)
		goCtx = context.WithValue(goCtx, constant.ContextKeyUserID, claims.Subject)
		goCtx = context.WithValue(goCtx, constant.ContextKeyLevel, claims.Level)

		if options.injectAccessKey {
			c.Set(constant.ContextKeyAdminAccessKey, accessKey)
			// SRE Note: Inject accessKey vào Go standard context
			goCtx = context.WithValue(goCtx, constant.ContextKeyAdminAccessKey, accessKey)
		}
		if options.injectAccessSecret {
			c.Set(constant.ContextKeyAdminAccessSecret, accessSecret)
			// SRE Note: Inject accessSecret vào Go standard context
			goCtx = context.WithValue(goCtx, constant.ContextKeyAdminAccessSecret, accessSecret)
		}
		if options.injectTokenJTI {
			c.Set(constant.ContextKeyAdminTokenJTI, strings.TrimSpace(claims.TokenID))
			// SRE Note: Inject tokenJTI vào Go standard context
			goCtx = context.WithValue(goCtx, constant.ContextKeyAdminTokenJTI, strings.TrimSpace(claims.TokenID))
		}
		if options.injectZoneID {
			c.Set(constant.ContextKeyAdminZoneID, strings.TrimSpace(claims.ZoneID))
			// SRE Note: Inject zoneID vào Go standard context
			goCtx = context.WithValue(goCtx, constant.ContextKeyAdminZoneID, strings.TrimSpace(claims.ZoneID))
		}
		c.Request = c.Request.WithContext(goCtx)

		// --------------------------------------------------------------------
		// 🔄 Tính thời gian hết hạn còn lại của session (tính bằng giây)
		//   và gửi qua Header phản hồi để Frontend chủ động hiển thị bộ đếm
		//   ngược hoặc cảnh báo hết hạn session cho Admin (Session Timeout warning).
		// Nó phụ vụ quá trình renew trinity access ở admin UI
		// --------------------------------------------------------------------
		expiresIn := claims.ExpiresAt - time.Now().Unix()
		if expiresIn < 0 {
			expiresIn = 0
		}
		c.Header("X-Session-Expires-In", strconv.FormatInt(expiresIn, 10))

		c.Next()
	}
}

// readAdminCookie đọc và chuẩn hóa cookie. Trả về generic 401 nếu lỗi.
func readAdminCookie(c *gin.Context, name string) (string, bool) {
	value, err := c.Cookie(name)
	if err != nil || strings.TrimSpace(value) == "" {
		// Log warning chi tiết để SRE debug xem cookie nào bị thiếu
		logger.HandlerWarn(c, "admin.auth.cookie", err, "missing or empty admin cookie: "+name)
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
