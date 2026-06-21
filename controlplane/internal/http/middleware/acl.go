// ============================================================================
// 🛡️ ARCHITECTURAL & SYSTEM CONTRACTS - ACCESS CONTROL LIFECYCLE (ACL) MIDDLEWARE
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
//   - Đóng vai trò Authentication Gate (Access Control Lifecycle - ACL) của tầng HTTP.
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
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	coreEntity "controlplane/internal/core/domain/entity"
	middlewareMetrics "controlplane/internal/http/middleware/metrics"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"controlplane/internal/security"
	"controlplane/pkg/apires"
	"controlplane/pkg/constant"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

// SessionRefreshService định nghĩa các hàm gia hạn/khôi phục phiên phục vụ tự động refresh ngầm.
type SessionRefreshService interface {
	RefreshUserOpaque(ctx context.Context, refreshToken string) (*iamEntity.RefreshTokenResult, error)
	RefreshUserTrinity(ctx context.Context, userID uuid.UUID, accessKey, accessSecret string) (*iamEntity.TrinityRefreshResult, error)
}

// aclMiddleware nắm giữ dependency runtime cho middleware ACL (Access Control Lifecycle).
var aclMiddleware = struct {
	mu          sync.RWMutex
	registry    *cacheengine.CacheRegistry
	graceWindow time.Duration
	touchFn     TouchDeviceLastSeenFn
	refreshSvc  SessionRefreshService
	cfg         *config.Config
}{}

// TouchDeviceLastSeenFn là kiểu hàm inject vào middleware để flush IP/UA xuống DB khi sự kiện last-seen fire.
type TouchDeviceLastSeenFn func(ctx context.Context, trackedDeviceID string, ip *string, userAgent *string)

// InitACL khởi tạo middleware ACL (Access Control Lifecycle) với CacheRegistry và hàm flush last-seen xuống DB.
func InitACL(
	registry *cacheengine.CacheRegistry,
	graceWindow time.Duration,
	touchFn TouchDeviceLastSeenFn,
	refreshSvc SessionRefreshService,
	cfg *config.Config,
) {
	aclMiddleware.mu.Lock()
	aclMiddleware.registry = registry
	aclMiddleware.graceWindow = graceWindow
	aclMiddleware.touchFn = touchFn
	aclMiddleware.refreshSvc = refreshSvc
	aclMiddleware.cfg = cfg
	aclMiddleware.mu.Unlock()
}

// userAccessSession là struct nội bộ ánh xạ bản ghi phiên lưu trong Redis.
type userAccessSession struct {
	AccessSecretHash string // Hash của Access secret để so khớp chữ ký
	TrackedDeviceID  string // ID thiết bị đã qua kiểm tra
	LastSeenAt       int64  // Unix timestamp — được middleware ghi lại sau mỗi request xác thực thành công
}

// aclOptions cấu hình các trường tùy chọn sẽ được inject vào Go context.
type aclOptions struct {
	injectAccessKey     bool
	injectAccessSecret  bool
	injectTokenJTI      bool
	injectTrackedDevice bool
}

// ACLOption định nghĩa hàm cấu hình tùy chọn cho ACL (Access Control Lifecycle) middleware.
type ACLOption func(*aclOptions)

// WithInjectAccessKey kích hoạt tiêm AccessKey vào Identity context struct.
func WithInjectAccessKey() ACLOption {
	return func(o *aclOptions) {
		o.injectAccessKey = true
	}
}

// WithInjectAccessSecret kích hoạt tiêm AccessSecret vào Identity context struct.
func WithInjectAccessSecret() ACLOption {
	return func(o *aclOptions) {
		o.injectAccessSecret = true
	}
}

// WithInjectTokenJTI kích hoạt tiêm Token JTI (JWT ID) vào Identity context struct.
func WithInjectTokenJTI() ACLOption {
	return func(o *aclOptions) {
		o.injectTokenJTI = true
	}
}

// WithInjectTrackedDevice kích hoạt tiêm TrackedDeviceID vào Identity context struct.
func WithInjectTrackedDevice() ACLOption {
	return func(o *aclOptions) {
		o.injectTrackedDevice = true
	}
}

// ACL trả về gin.HandlerFunc xác thực JWT, tra cứu Redis và tiêm Identity struct vào Go context.
// Tên đại diện cho Access Control Lifecycle (ACL).
func ACL(opts ...ACLOption) gin.HandlerFunc {
	// Parse các cấu hình tùy chọn nhận vào
	options := &aclOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(options)
		}
	}

	const cookiePath = "/"
	const cookieDomain = ""

	return func(c *gin.Context) {
		// Đọc cấu hình runtime đã lưu từ InitACL
		aclMiddleware.mu.RLock()
		registry := aclMiddleware.registry
		touchFn := aclMiddleware.touchFn
		aclMiddleware.mu.RUnlock()

		if registry == nil {
			middlewareMetrics.RecordAuthAttempt("access", "service_unavailable", "uninitialized_middleware")
			apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
			c.Abort()
			return
		}

		// --------------------------------------------------------------------
		// BƯỚC 1: LẤY SIGNING SECRET TỪ CACHE REGISTRY (Đưa lên đầu để phục vụ parse/refresh)
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

		// --------------------------------------------------------------------
		// BƯỚC 2: LẤY ACCESS TOKEN TỪ HEADER HOẶC COOKIE & PARSE JWT
		// --------------------------------------------------------------------
		var rawToken string
		if bearer, ok := security.ExtractBearerToken(c.GetHeader("Authorization")); ok {
			rawToken = bearer
		} else if cookieToken, err := c.Cookie(constant.AccessTokenName); err == nil {
			rawToken = strings.TrimSpace(cookieToken)
		}

		var (
			claims      security.Claims
			parsed      bool
			parseErr    error
			emptySecret bool
		)

		if rawToken != "" {
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
		}

		// [COMMENT]: Nếu access_token bị thiếu hoặc đã hết hạn -> Thử Silent Opaque Refresh (Kiểu 2)
		if rawToken == "" || (!parsed && !emptySecret) {
			refreshTokenCookie, err := c.Cookie(constant.RefreshTokenName)
			if err == nil && strings.TrimSpace(refreshTokenCookie) != "" && aclMiddleware.refreshSvc != nil {
				ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
				result, refreshErr := aclMiddleware.refreshSvc.RefreshUserOpaque(ctx, strings.TrimSpace(refreshTokenCookie))
				cancel()
				if refreshErr == nil && result != nil {
					secure := isSecureRequest(c)
					var domain string
					if aclMiddleware.cfg != nil {
						domain = strings.TrimSpace(aclMiddleware.cfg.App.PublicDomain)
					}
					setUserCookies(c, domain, secure, result.AccessToken, result.RefreshToken, result.AccessKey, result.AccessSecret, result.AccessExpiresAt, result.RefreshExpiresAt)

					// Cập nhật lại rawToken và giải mã bằng các khóa ký
					rawToken = result.AccessToken
					for _, candidate := range []coreEntity.RuntimeSecret{secrets.Active, secrets.Standby} {
						claims, parseErr = security.Parse(rawToken, candidate.Secret)
						if parseErr == nil {
							parsed = true
							break
						}
					}
				}
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

		// [COMMENT]: Trinity Refresh (Kiểu 1) - Tự động gia hạn khi token còn sống nhưng sắp hết hạn (≤ 900s)
		now := time.Now().Unix()
		if claims.ExpiresAt-now <= 900 && aclMiddleware.refreshSvc != nil {
			if accessSecret != "" {
				userID, parseUUIDErr := uuid.Parse(userIDClaim)
				if parseUUIDErr == nil {
					ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
					result, refreshErr := aclMiddleware.refreshSvc.RefreshUserTrinity(ctx, userID, accessKeyClaim, accessSecret)
					cancel()
					if refreshErr == nil && result != nil {
						secure := isSecureRequest(c)
						var domain string
						if aclMiddleware.cfg != nil {
							domain = strings.TrimSpace(aclMiddleware.cfg.App.PublicDomain)
						}
						setUserCookies(c, domain, secure, result.AccessToken, "", result.AccessKey, result.AccessSecret, result.AccessExpiresAt, time.Time{})

						// Giải mã token mới để lấy các claims và secret mới
						for _, candidate := range []coreEntity.RuntimeSecret{secrets.Active, secrets.Standby} {
							newClaims, parseErr := security.Parse(result.AccessToken, candidate.Secret)
							if parseErr == nil {
								claims = newClaims
								accessKeyClaim = strings.TrimSpace(claims.AccessKey)
								userIDClaim = strings.TrimSpace(claims.Subject)
								jti = strings.TrimSpace(claims.TokenID)
								accessSecret = result.AccessSecret
								accessKeyFromCookie = result.AccessKey
								secretCookieErr = nil
								keyCookieErr = nil
								break
							}
						}
					}
				}
			}
		}

		if secretCookieErr != nil || keyCookieErr != nil || accessSecret == "" || accessKeyFromCookie == "" {
			middlewareMetrics.RecordAuthAttempt("access", "unauthorized", "missing_cookie_credentials")
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}

		if accessKeyFromCookie != accessKeyClaim {
			middlewareMetrics.RecordAuthAttempt("access", "unauthorized", "mismatched_key")
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
		// [COMMENT]: BƯỚC 6 — Cập nhật LastSeenAt trong Redis.
		// CRITICAL FIX: Phải dùng proto.Marshal (protobuf binary) thay vì json.Marshal
		// vì BƯỚC 4 đọc session bằng proto.Unmarshal. Nếu ghi JSON ở đây, request tiếp theo
		// sẽ proto.Unmarshal fail → toàn bộ session bị vô hiệu hoá sau 30 giây đầu tiên.
		now = time.Now().Unix()
		if now-record.LastSeenAt > 30 {
			clientIP := c.ClientIP()
			userAgent := c.Request.UserAgent()
			trackedDeviceID := strings.TrimSpace(record.TrackedDeviceID)

			// Tạo protobuf message mới với LastSeenAt đã cập nhật
			updatedPb := &iamproto.UserAccessSession{
				Ash:  record.AccessSecretHash,
				Tdid: record.TrackedDeviceID,
				Lsa:  now,
			}
			if payload, marshalErr := proto.Marshal(updatedPb); marshalErr == nil {
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

// isSecureRequest kiểm tra xem request hiện tại có bảo mật (HTTPS) hay không.
func isSecureRequest(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}
	if strings.ToLower(c.GetHeader("X-Forwarded-Proto")) == "https" {
		return true
	}
	return false
}

// setUserCookies lưu bộ cookie định danh phiên mới vào HTTP response.
func setUserCookies(
	c *gin.Context,
	domain string,
	secure bool,
	accessToken string,
	refreshToken string,
	accessKey string,
	accessSecret string,
	accessExpiresAt time.Time,
	refreshExpiresAt time.Time,
) {
	setCookie := func(name, val string, expires time.Time, httpOnly bool) {
		if val == "" || expires.IsZero() {
			return
		}
		http.SetCookie(c.Writer, &http.Cookie{
			Name:     name,
			Value:    val,
			Path:     "/",
			Domain:   domain,
			HttpOnly: httpOnly,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
			Expires:  expires,
			MaxAge:   int(time.Until(expires).Seconds()),
		})
	}
	setCookie(constant.AccessTokenName, accessToken, accessExpiresAt, true)
	setCookie(constant.RefreshTokenName, refreshToken, refreshExpiresAt, true)
	setCookie(constant.AccessKeyName, accessKey, accessExpiresAt, false)
	setCookie(constant.AccessSecretName, accessSecret, accessExpiresAt, true)
}
