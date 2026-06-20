// ============================================================================
// 🛡️ ARCHITECTURAL & SYSTEM CONTRACTS - ACCESS MIDDLEWARE
// ============================================================================
//
// 🤝 1. SYSTEM CONTRACT
//   - Token Extraction: Trích xuất Bearer token từ Authorization header.
//     Nếu không có, fallback sang cookie `access_token` để hỗ trợ browser-based flow.
//   - Session Binding: Sau khi parse JWT lấy ra access_key, tra bản ghi phiên trong
//     Redis thông qua CacheEngine L2 (không dùng rdb trực tiếp) để xác thực
//     access_key + access_secret_hash có khớp hay không.
//   - Fail-Closed: Khi thiếu dependencies hoặc lỗi kết nối Redis → 503 Service Unavailable.
//
// 📖 2. SOURCE OF TRUTH
//   - Signing secret: lấy từ CacheRegistry (L1 cache → loader → DB).
//   - Session state: lưu tại Redis dưới key `iam:user_access_session:<userID>:<accessKey>`.
//     Được truy vấn qua CacheEngine L2 để giữ abstraction nhất quán.
//
// 🚧 3. SYSTEM BOUNDARY
//   - Đóng vai trò Authentication Gate của tầng HTTP.
//   - Chỉ xác thực danh tính và tiêm thông tin định danh vào Go context.
//     Không thực hiện authorization hoặc RBAC ở đây.
//
// 💡 4. OPTION PATTERN
//   - Hỗ trợ các options (WithInjectAccessKey, WithInjectAccessSecret, v.v.)
//     để chọn lựa tiêm các trường tùy chọn vào struct Identity.
//   - Chỉ 1 lần duy nhất ValueContext.WithValue được gọi, triệt tiêu chain inflation.
//
// ============================================================================

package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"controlplane/internal/cacheengine"
	coreEntity "controlplane/internal/core/domain/entity"
	middlewareMetrics "controlplane/internal/http/middleware/metrics"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"controlplane/internal/security"
	"controlplane/pkg/apires"
	"controlplane/pkg/constant"

	"github.com/gin-gonic/gin"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

// accessMiddleware nắm giữ dependency runtime cho middleware Access.
var accessMiddleware = struct {
	mu          sync.RWMutex
	registry    *cacheengine.CacheRegistry
	graceWindow time.Duration
	touchFn     TouchDeviceLastSeenFn
}{}

// TouchDeviceLastSeenFn là kiểu hàm inject vào middleware để flush IP/UA xuống DB khi sự kiện last-seen fire.
type TouchDeviceLastSeenFn func(ctx context.Context, trackedDeviceID string, ip *string, userAgent *string)

// InitAccess khởi tạo middleware Access với CacheRegistry và hàm flush last-seen xuống DB.
func InitAccess(registry *cacheengine.CacheRegistry, graceWindow time.Duration, touchFn TouchDeviceLastSeenFn) {
	accessMiddleware.mu.Lock()
	accessMiddleware.registry = registry
	accessMiddleware.graceWindow = graceWindow
	accessMiddleware.touchFn = touchFn
	accessMiddleware.mu.Unlock()
}

// userAccessSession là struct nội bộ ánh xạ bản ghi phiên lưu trong Redis.
type userAccessSession struct {
	AccessSecretHash string `json:"ash"`  // Hash của Access secret để so khớp chữ ký
	TrackedDeviceID  string `json:"tdid"` // ID thiết bị đã qua kiểm tra
	LastSeenAt       int64  `json:"lsa"`  // Unix timestamp — được middleware ghi lại sau mỗi request xác thực thành công
}

// accessOptions cấu hình các trường tùy chọn sẽ được inject vào Go context.
type accessOptions struct {
	injectAccessKey     bool
	injectAccessSecret  bool
	injectTokenJTI      bool
	injectTrackedDevice bool
}

// AccessOption định nghĩa hàm cấu hình tùy chọn cho Access middleware.
type AccessOption func(*accessOptions)

// WithInjectAccessKey kích hoạt tiêm AccessKey vào Identity context struct.
func WithInjectAccessKey() AccessOption {
	return func(o *accessOptions) {
		o.injectAccessKey = true
	}
}

// WithInjectAccessSecret kích hoạt tiêm AccessSecret vào Identity context struct.
func WithInjectAccessSecret() AccessOption {
	return func(o *accessOptions) {
		o.injectAccessSecret = true
	}
}

// WithInjectTokenJTI kích hoạt tiêm Token JTI (JWT ID) vào Identity context struct.
func WithInjectTokenJTI() AccessOption {
	return func(o *accessOptions) {
		o.injectTokenJTI = true
	}
}

// WithInjectTrackedDevice kích hoạt tiêm TrackedDeviceID vào Identity context struct.
func WithInjectTrackedDevice() AccessOption {
	return func(o *accessOptions) {
		o.injectTrackedDevice = true
	}
}

// Access trả về gin.HandlerFunc xác thực JWT, tra cứu Redis và tiêm Identity struct vào Go context.
func Access(opts ...AccessOption) gin.HandlerFunc {
	// Parse các cấu hình tùy chọn nhận vào
	options := &accessOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(options)
		}
	}

	const cookiePath = "/"
	const cookieDomain = ""

	return func(c *gin.Context) {
		// Đọc cấu hình runtime đã lưu từ InitAccess
		accessMiddleware.mu.RLock()
		registry := accessMiddleware.registry
		touchFn := accessMiddleware.touchFn
		accessMiddleware.mu.RUnlock()

		if registry == nil {
			middlewareMetrics.RecordAuthAttempt("access", "service_unavailable", "uninitialized_middleware")
			apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
			c.Abort()
			return
		}

		// --------------------------------------------------------------------
		// BƯỚC 1: LẤY ACCESS TOKEN TỪ HEADER HOẶC COOKIE
		// --------------------------------------------------------------------
		var rawToken string
		if bearer, ok := security.ExtractBearerToken(c.GetHeader("Authorization")); ok {
			rawToken = bearer
		} else if cookieToken, err := c.Cookie(constant.AccessTokenName); err == nil {
			rawToken = strings.TrimSpace(cookieToken)
		}
		if rawToken == "" {
			middlewareMetrics.RecordAuthAttempt("access", "unauthorized", "missing_token")
			c.Header("WWW-Authenticate", "Bearer")
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}

		// --------------------------------------------------------------------
		// BƯỚC 2: LẤY SIGNING SECRET TỪ CACHE REGISTRY → PARSE JWT
		// --------------------------------------------------------------------
		secretVal, err := registry.GetOrLoad(c.Request.Context(), "access_secret", "")
		if err != nil {
			middlewareMetrics.RecordCacheOperation("access", "access_secret", "L1_L2", "error")
			middlewareMetrics.RecordAuthAttempt("access", "service_unavailable", "db_error")
			apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
			c.Abort()
			return
		}
		secrets, ok := secretVal.(*coreEntity.RuntimeSecrets)
		if !ok || secrets == nil {
			middlewareMetrics.RecordCacheOperation("access", "access_secret", "L1_L2", "error")
			middlewareMetrics.RecordAuthAttempt("access", "service_unavailable", "db_error")
			apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
			c.Abort()
			return
		}
		middlewareMetrics.RecordCacheOperation("access", "access_secret", "L1_L2", "hit")

		var (
			claims      security.Claims
			parsed      bool
			parseErr    error
			emptySecret bool
		)
		for _, candidate := range []coreEntity.RuntimeSecret{secrets.Active, secrets.Standby} {
			claims, parseErr = security.Parse(rawToken, candidate.Secret)
			if parseErr == nil {
				parsed = true
				break
			}
			if errors.Is(parseErr, security.ErrEmptySecret) {
				emptySecret = true
			}
		}
		if !parsed {
			if emptySecret {
				middlewareMetrics.RecordAuthAttempt("access", "service_unavailable", "empty_secret")
				apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
				c.Abort()
				return
			}
			middlewareMetrics.RecordAuthAttempt("access", "unauthorized", "invalid_token")
			c.Header("WWW-Authenticate", "Bearer")
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}

		// --------------------------------------------------------------------
		// BƯỚC 3: TRÍCH XUẤT CLAIMS & COOKIES CƠ BẢN
		// --------------------------------------------------------------------
		accessKeyClaim := strings.TrimSpace(claims.AccessKey)
		userIDClaim := strings.TrimSpace(claims.Subject)
		jti := strings.TrimSpace(claims.TokenID)

		if accessKeyClaim == "" || userIDClaim == "" || jti == "" {
			middlewareMetrics.RecordAuthAttempt("access", "unauthorized", "missing_claims")
			c.Header("WWW-Authenticate", "Bearer")
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}

		accessSecretCookie, secretCookieErr := c.Cookie(constant.AccessSecretName)
		accessKeyCookie, keyCookieErr := c.Cookie(constant.AccessKeyName)
		accessSecret := strings.TrimSpace(accessSecretCookie)
		accessKeyFromCookie := strings.TrimSpace(accessKeyCookie)

		if secretCookieErr != nil || keyCookieErr != nil || accessSecret == "" || accessKeyFromCookie == "" {
			middlewareMetrics.RecordAuthAttempt("access", "unauthorized", "missing_cookie_credentials")
			clearUserAccessCookies(c, cookieDomain, cookiePath)
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}

		if accessKeyFromCookie != accessKeyClaim {
			middlewareMetrics.RecordAuthAttempt("access", "unauthorized", "mismatched_key")
			clearUserAccessCookies(c, cookieDomain, cookiePath)
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}

		// --------------------------------------------------------------------
		// BƯỚC 4: XÁC THỰC PHIÊN QUA REDIS L2 (SESSION BINDING)
		// --------------------------------------------------------------------
		ctx, cancel := context.WithTimeout(c.Request.Context(), 100*time.Millisecond)
		defer cancel()

		sessionKey := "iam:user_access_session:" + userIDClaim + ":" + accessKeyClaim
		rawPayload, _, exists, l2Err := registry.L2.Get(ctx, sessionKey)
		if l2Err != nil {
			middlewareMetrics.RecordCacheOperation("access", "user_access_session", "L2", "error")
			middlewareMetrics.RecordAuthAttempt("access", "service_unavailable", "redis_error")
			apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
			c.Abort()
			return
		}
		if !exists {
			middlewareMetrics.RecordCacheOperation("access", "user_access_session", "L2", "miss")
			middlewareMetrics.RecordAuthAttempt("access", "unauthorized", "session_not_found")
			clearUserAccessCookies(c, cookieDomain, cookiePath)
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}
		middlewareMetrics.RecordCacheOperation("access", "user_access_session", "L2", "hit")

		var pb iamproto.UserAccessSession
		if err := proto.Unmarshal(rawPayload, &pb); err != nil {
			middlewareMetrics.RecordAuthAttempt("access", "service_unavailable", "invalid_session_payload")
			apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
			c.Abort()
			return
		}
		record := userAccessSession{
			AccessSecretHash: pb.Ash,
			TrackedDeviceID:  pb.Tdid,
			LastSeenAt:       pb.Lsa,
		}

		// Đối khớp access_secret cookie với hash lưu trong Redis
		if record.AccessSecretHash != security.HashTokenSHA256(accessSecret) {
			middlewareMetrics.RecordAuthAttempt("access", "unauthorized", "invalid_secret")
			clearUserAccessCookies(c, cookieDomain, cookiePath)
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}

		middlewareMetrics.RecordAuthAttempt("access", "success", "none")

		// --------------------------------------------------------------------
		// BƯỚC 5: TẠO STRUCT IDENTITY VÀ INJECT VÀO GO CONTEXT
		// --------------------------------------------------------------------
		// Nhóm các thông tin cơ bản luôn luôn được inject
		ident := &constant.Identity{
			UserID:   userIDClaim,
			Role:     claims.Role,
			TenantID: claims.TenantID,
			Level:    claims.Level,
			ZoneID:   claims.ZoneID,
		}

		// Inject các thông tin tùy chọn theo cấu hình để tránh phình context
		if options.injectAccessKey {
			ident.AccessKey = accessKeyClaim
		}
		if options.injectAccessSecret {
			ident.AccessSecret = accessSecret
		}
		if options.injectTokenJTI {
			ident.JTI = jti
		}
		if options.injectTrackedDevice {
			if trackedDevice := strings.TrimSpace(record.TrackedDeviceID); trackedDevice != "" {
				ident.TrackedDeviceID = trackedDevice
			}
		}

		// Tiêm Identity vào Go standard context
		goCtx := context.WithValue(c.Request.Context(), constant.IdentityKey, ident)
		c.Request = c.Request.WithContext(goCtx)

		// [COMMENT]: Gửi header X-Session-Expires-In (giây) để Frontend biết session còn sống bao lâu.
		// Frontend dùng giá trị này để tự động gọi trinity-refresh khi ≤ 900s còn lại.
		ttlCtx, ttlCancel := context.WithTimeout(c.Request.Context(), 50*time.Millisecond)
		ttlResult, ttlErr := registry.L2.Client().TTL(ttlCtx, sessionKey).Result()
		ttlCancel()
		if ttlErr == nil && ttlResult > 0 {
			c.Header("X-Session-Expires-In", fmt.Sprintf("%d", int64(ttlResult.Seconds())))
		}

		c.Next()

		// --------------------------------------------------------------------
		// BƯỚC 6: CẬP NHẬT LAST SEEN (BEST-EFFORT, HAI TẦNG THROTTLE)
		// --------------------------------------------------------------------
		now := time.Now().Unix()
		if now-record.LastSeenAt > 30 {
			clientIP := c.ClientIP()
			userAgent := c.Request.UserAgent()
			trackedDeviceID := strings.TrimSpace(record.TrackedDeviceID)

			updatedRecord := record
			updatedRecord.LastSeenAt = now
			if payload, marshalErr := json.Marshal(updatedRecord); marshalErr == nil {
				rdb := registry.L2.Client()
				dbFlushGateKey := "iam:user_lsadb:" + userIDClaim + ":" + accessKeyClaim
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
					defer cancel()

					// Cập nhật lsa trong Redis, giữ nguyên TTL gốc của session
					_ = rdb.Set(ctx, sessionKey, payload, goredis.KeepTTL).Err()

					// Chỉ flush DB nếu SET NX thành công (tần suất ~3 phút/lần)
					if touchFn != nil && trackedDeviceID != "" {
						won, _ := rdb.SetNX(ctx, dbFlushGateKey, 1, 3*time.Minute).Result()
						if won {
							ip := &clientIP
							ua := &userAgent
							touchFn(ctx, trackedDeviceID, ip, ua)
						}
					}
				}()
			}
		}
	}
}

// clearUserAccessCookies xóa cookie xác thực phía client
func clearUserAccessCookies(c *gin.Context, domain, path string) {
	if c == nil {
		return
	}
	exp := time.Unix(0, 0)
	for _, name := range []string{
		constant.AccessTokenName,
		constant.AccessKeyName,
		constant.AccessSecretName,
	} {
		http.SetCookie(c.Writer, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     path,
			Domain:   domain,
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
			Expires:  exp,
		})
	}
}
