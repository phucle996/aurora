// ============================================================================
// 🛡️ ARCHITECTURAL & SYSTEM CONTRACTS
// ============================================================================
//
// 🤝 1. SYSTEM CONTRACT
//   - Token Extraction: Trích xuất Bearer token từ Authorization header. Không chấp nhận fallback
//     sang cookie để duy trì tính nhất quán cho luồng API xác thực.
//   - Device Binding: Khi bật runtimeCache, xác thực sự khớp nhau giữa claims trong JWT và cặp cookie
//     (access_key, access_secret) so với bản ghi lưu trữ trên Redis thông qua graceWindow.
//   - Fail-Closed: Khi thiếu các runtime dependencies hoặc xảy ra lỗi kết nối Redis, hệ thống sẽ trả
//     về lỗi 503 Service Unavailable để tránh bỏ lọt request lỗi.
//
// 📖 2. SOURCE OF TRUTH
//   - Danh sách Candidate Secrets thu nhận từ SecretProvider để thực hiện giải mã token.
//   - Trạng thái thu hồi session (blacklist) được kiểm tra trực tiếp qua Redis.
//
// 🚧 3. SYSTEM BOUNDARY
//   - Đóng vai trò là cửa ngõ kiểm soát danh tính (Authentication Gate) của tầng HTTP.
//   - Chỉ thực hiện trích xuất, xác thực và tiêm các thông tin cơ bản vào context để các service nghiệp
//     vụ và handler phía sau tiêu thụ.
//
// 💡 4. OPERATIONAL NOTES
//   - Hiệu năng (Local Blacklist Cache): Sử dụng bộ nhớ trong `revokedJTICache` để lưu tạm các token đã bị
//     thu hồi (revoked = true) trong 5 phút. Không cache token hợp lệ để đảm bảo thao tác đăng xuất/thu hồi
//     session lập tức có hiệu lực trên toàn cụm.

package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"controlplane/internal/cacheengine"
	coreEntity "controlplane/internal/core/domain/entity"
	iamCache "controlplane/internal/iam/cache"
	"controlplane/internal/security"
	"controlplane/pkg/apires"
	"controlplane/pkg/constant"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	blacklistKeyPrefix    = "iam:blacklist:"
	blacklistCheckTimeout = 75 * time.Millisecond
	revokedJTICacheTTL    = 5 * time.Minute
)

// revokedJTICache lưu trữ cục bộ các token đã bị thu hồi để giảm tải lượng truy vấn lớn tới Redis.
var revokedJTICache = struct {
	mu        sync.RWMutex
	expiresAt map[string]time.Time
}{
	expiresAt: make(map[string]time.Time),
}

// accessMiddleware nắm giữ handler động cho middleware Access.
var accessMiddleware = struct {
	mu      sync.RWMutex
	handler gin.HandlerFunc
}{}

// InitAccess khởi tạo dependencies cho middleware Access.
func InitAccess(registry *cacheengine.CacheRegistry, rdb *redis.Client,
	runtimeCache iamCache.UserDeviceRuntimeCache, graceWindow time.Duration) {
	accessMiddleware.mu.Lock()
	accessMiddleware.handler = buildAccessHandler(registry, rdb, runtimeCache, graceWindow)
	accessMiddleware.mu.Unlock()
}

// Access định tuyến yêu cầu xác thực sang handler đang hoạt động.
func Access() gin.HandlerFunc {
	accessMiddleware.mu.RLock()
	handler := accessMiddleware.handler
	accessMiddleware.mu.RUnlock()
	if handler == nil {
		return func(c *gin.Context) {
			apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
			c.Abort()
		}
	}
	return handler
}

// buildAccessHandler xây dựng hàm xác thực JWT, so khớp cookie và inject context.
func buildAccessHandler(registry *cacheengine.CacheRegistry, rdb *redis.Client, runtimeCache iamCache.UserDeviceRuntimeCache, graceWindow time.Duration) gin.HandlerFunc {
	if graceWindow <= 0 {
		graceWindow = 10 * time.Second
	}
	cookiePath := "/"
	cookieDomain := ""

	return func(c *gin.Context) {
		// --------------------------------------------------------------------
		// 🔄 Trích xuất Bearer token từ Authorization header.
		// --------------------------------------------------------------------
		token, ok := security.ExtractBearerToken(c.GetHeader("Authorization"))
		if !ok {
			c.Header("WWW-Authenticate", "Bearer")
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}

		// --------------------------------------------------------------------
		// 🔄 Kiểm tra xem CacheRegistry đã được khởi tạo thành công chưa.
		// --------------------------------------------------------------------
		if registry == nil {
			apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
			c.Abort()
			return
		}

		// --------------------------------------------------------------------
		// 🔄 Lấy danh sách candidate secrets để giải mã token hỗ trợ rotation.
		// --------------------------------------------------------------------
		val, err := registry.GetOrLoad(c.Request.Context(), "access_secret", "")
		if err != nil {
			apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
			c.Abort()
			return
		}
		secrets, ok := val.(*coreEntity.RuntimeSecrets)
		if !ok || secrets == nil {
			apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
			c.Abort()
			return
		}

		var (
			claims      security.Claims
			parsed      bool
			parseErr    error
			emptySecret bool
		)
		
		// --------------------------------------------------------------------
		// 🔄 Parse JWT sử dụng lần lượt các candidate secrets.
		// --------------------------------------------------------------------
		candidates := []coreEntity.RuntimeSecret{secrets.Active, secrets.Standby}
		for _, candidate := range candidates {
			claims, parseErr = security.Parse(token, candidate.Secret)
			if parseErr == nil {
				parsed = true
				break
			}
			if errors.Is(parseErr, security.ErrEmptySecret) {
				emptySecret = true
			}
		}
		if !parsed {
			// Lỗi cấu hình secret rỗng là lỗi hệ thống (503), lỗi parse sai là unauthorized (401).
			if emptySecret {
				apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
				c.Abort()
				return
			}
			c.Header("WWW-Authenticate", "Bearer")
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}

		// --------------------------------------------------------------------
		// 🔄 Kiểm tra định danh Token (JTI) xem có bị nằm trong blacklist của Redis.
		// --------------------------------------------------------------------
		blacklisted, blacklistErr := IsBlacklisted(c.Request.Context(), rdb, claims.TokenID)
		if blacklistErr != nil {
			logger.HandlerWarn(c, "iam.access", blacklistErr, "redis blacklist check failed")
			apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
			c.Abort()
			return
		}
		if blacklisted {
			logger.HandlerWarn(c, "iam.access", nil, "token is blacklisted")
			c.Header("WWW-Authenticate", "Bearer")
			apires.RespondUnauthorized(c, "token has been revoked")
			c.Abort()
			return
		}

		// --------------------------------------------------------------------
		// 🔄 Xác minh ràng buộc phiên thiết bị của người dùng (Device Binding)
		// --------------------------------------------------------------------
		if runtimeCache != nil {
			accessKeyCookieValue, accessKeyCookieErr := c.Cookie(constant.AccessKeyName)
			accessSecretValue, accessSecretErr := c.Cookie(constant.AccessSecretName)
			accessKeyCookie := strings.TrimSpace(accessKeyCookieValue)
			accessSecret := strings.TrimSpace(accessSecretValue)
			accessKeyClaim := strings.TrimSpace(claims.AccessKey)
			jti := strings.TrimSpace(claims.TokenID)
			
			// Đảm bảo có đầy đủ dữ liệu cấu thành phiên đăng nhập thiết bị:
			if accessKeyCookieErr != nil || accessSecretErr != nil || accessKeyCookie == "" || accessSecret == "" || accessKeyClaim == "" || jti == "" || strings.TrimSpace(claims.Subject) == "" {
				clearUserAccessCookies(c, cookieDomain, cookiePath)
				apires.RespondUnauthorized(c, "unauthorized")
				c.Abort()
				return
			}
			
			// Key định danh từ cookie phải khớp với AccessKey được mã hóa trong token:
			if accessKeyCookie != accessKeyClaim {
				clearUserAccessCookies(c, cookieDomain, cookiePath)
				apires.RespondUnauthorized(c, "unauthorized")
				c.Abort()
				return
			}

			ctx, cancel := context.WithTimeout(c.Request.Context(), 100*time.Millisecond)
			defer cancel()
			
			// Truy vấn bản ghi phiên runtime thực tế từ Redis:
			record, err := runtimeCache.GetDeviceRuntimeByUserDevice(ctx, claims.Subject, accessKeyClaim)
			if err != nil {
				apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
				c.Abort()
				return
			}
			
			// Đánh giá khớp thông tin mật mã và thời gian đồng bộ graceWindow:
			if record == nil || !iamCache.MatchRuntime(record, accessKeyCookie, accessSecret, jti, graceWindow) {
				clearUserAccessCookies(c, cookieDomain, cookiePath)
				apires.RespondUnauthorized(c, "unauthorized")
				c.Abort()
				return
			}
			if strings.TrimSpace(record.TrackedDeviceID) != "" {
				c.Set(constant.ContextKeyTrackedDeviceID, strings.TrimSpace(record.TrackedDeviceID))
			}
		}

		// --------------------------------------------------------------------
		// 🔄 Tiêm toàn bộ claims và các trường định danh phẳng vào Gin Context.
		// --------------------------------------------------------------------
		c.Set(constant.ContextKeyJWTClaims, claims)
		c.Set(constant.ContextKeyUserID, claims.Subject)
		c.Set(constant.ContextKeyRole, claims.Role)
		c.Set(constant.ContextKeyJTI, claims.TokenID)
		c.Set(constant.ContextKeyRuntimeAccessKey, claims.AccessKey)
		c.Set(constant.ContextKeyLevel, claims.Level)
		c.Set(constant.ContextKeyTenant, claims.TenantID)

		// Xác thực hoàn tất -> chuyển tiếp sang handler nghiệp vụ
		c.Next()
	}
}

func GetUserID(c *gin.Context) string {
	return getContextString(c, constant.ContextKeyUserID)
}

func GetTrackedDeviceID(c *gin.Context) string {
	return getContextString(c, constant.ContextKeyTrackedDeviceID)
}

func GetRuntimeAccessKey(c *gin.Context) string {
	return getContextString(c, constant.ContextKeyRuntimeAccessKey)
}

func getContextString(c *gin.Context, key string) string {
	if c == nil {
		return ""
	}
	v, ok := c.Get(key)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// clearUserAccessCookies xóa bỏ triệt để các session cookies của Admin/User khi phiên đăng nhập không hợp lệ.
func clearUserAccessCookies(c *gin.Context, cookieDomain, cookiePath string) {
	secure := c.Request.TLS != nil
	exp := time.Unix(0, 0)
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     constant.AccessKeyName,
		Value:    "",
		Path:     cookiePath,
		Domain:   cookieDomain,
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  exp,
	})
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     constant.AccessSecretName,
		Value:    "",
		Path:     cookiePath,
		Domain:   cookieDomain,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  exp,
	})
}

// IsBlacklisted kiểm tra nhanh xem JTI (JWT ID) có đang bị vô hiệu hóa trên Redis hay không.
func IsBlacklisted(ctx context.Context, rdb *redis.Client, jti string) (bool, error) {
	jti = strings.TrimSpace(jti)
	if jti == "" || rdb == nil {
		return false, nil
	}

	// 1. Kiểm tra nhanh tại Local Cache:
	now := time.Now().UTC()
	revokedJTICache.mu.RLock()
	cachedUntil, cachedRevoked := revokedJTICache.expiresAt[jti]
	revokedJTICache.mu.RUnlock()
	if cachedRevoked && now.Before(cachedUntil) {
		return true, nil
	}
	
	// Xóa bỏ cache đã quá hạn (eviction):
	if cachedRevoked {
		revokedJTICache.mu.Lock()
		if revokedJTICache.expiresAt[jti].Equal(cachedUntil) {
			delete(revokedJTICache.expiresAt, jti)
		}
		revokedJTICache.mu.Unlock()
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// 2. Truy vấn trực tiếp từ Redis Blacklist:
	checkCtx, cancel := context.WithTimeout(ctx, blacklistCheckTimeout)
	defer cancel()

	key := blacklistKeyPrefix + jti
	exists, err := rdb.Exists(checkCtx, key).Result()
	if err != nil {
		return false, err
	}
	if exists <= 0 {
		return false, nil
	}

	// 3. Ghi nhận JTI đã bị thu hồi vào local cache để tối ưu hóa truy cập tiếp theo:
	revokedJTICache.mu.Lock()
	revokedJTICache.expiresAt[jti] = now.Add(revokedJTICacheTTL)
	revokedJTICache.mu.Unlock()
	return true, nil
}
