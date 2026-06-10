// ============================================================================
// 🛡️ ARCHITECTURAL & SYSTEM CONTRACTS
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
//   - Chỉ xác thực danh tính và tiêm thông tin định danh vào context.
//     Không thực hiện authorization hoặc RBAC ở đây.
//
// 💡 4. LUỒNG XỬ LÝ
//   1. Lấy access token từ Authorization header hoặc cookie.
//   2. GetOrLoad access_secret từ CacheRegistry → parse JWT → lấy claims.AccessKey.
//   3. Lấy access_secret từ cookie.
//   4. Tra bản ghi phiên từ Redis L2 theo key = userID + accessKey từ claims.
//   5. So khớp access_key cookie với claims, hash access_secret, graceWindow JTI.
//   6. Tiêm claims + định danh phiên vào Gin context + Go context.

package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"controlplane/internal/cacheengine"
	coreEntity "controlplane/internal/core/domain/entity"
	"controlplane/internal/security"
	"controlplane/pkg/apires"
	"controlplane/pkg/constant"

	"github.com/gin-gonic/gin"
	goredis "github.com/redis/go-redis/v9"
)

// accessMiddleware nắm giữ handler động cho middleware Access.
var accessMiddleware = struct {
	mu      sync.RWMutex
	handler gin.HandlerFunc
}{}

// TouchDeviceLastSeenFn là kiểu hàm inject vào middleware để flush IP/UA xuống DB khi sự kiện last-seen fire.
// Thiết kế dạng function type để tránh coupling middleware với service/repo layer.
type TouchDeviceLastSeenFn func(ctx context.Context, trackedDeviceID string, ip *string, userAgent *string)

// InitAccess khởi tạo middleware Access với CacheRegistry và hàm flush last-seen xuống DB.
// touchFn có thể là nil — khi đó chỉ cập nhật Redis, không flush DB.
func InitAccess(registry *cacheengine.CacheRegistry, graceWindow time.Duration, touchFn TouchDeviceLastSeenFn) {
	accessMiddleware.mu.Lock()
	accessMiddleware.handler = buildAccessHandler(registry, graceWindow, touchFn)
	accessMiddleware.mu.Unlock()
}

// Access trả về gin.HandlerFunc đang hoạt động.
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

// userAccessSession là struct nội bộ ánh xạ bản ghi phiên lưu trong Redis.
// Chỉ giữ lại các trường thực sự cần dùng để đối chiếu chữ ký, định danh thiết bị, và realtime tracking.
type userAccessSession struct {
	AccessSecretHash string `json:"ash"`  // Hash của Access secret để so khớp chữ ký
	TrackedDeviceID  string `json:"tdid"` // ID thiết bị đã qua kiểm tra
	LastSeenAt       int64  `json:"lsa"`  // Unix timestamp — được middleware ghi lại sau mỗi request xác thực thành công
}

// buildAccessHandler xây dựng hàm xác thực JWT, tra phiên Redis, và inject context.
func buildAccessHandler(registry *cacheengine.CacheRegistry, graceWindow time.Duration, touchFn TouchDeviceLastSeenFn) gin.HandlerFunc {
	if graceWindow <= 0 {
		graceWindow = 10 * time.Second
	}
	const cookiePath = "/"
	const cookieDomain = ""

	return func(c *gin.Context) {
		// --------------------------------------------------------------------
		// BƯỚC 1: LẤY ACCESS TOKEN TỪ HEADER HOẶC COOKIE
		// Ưu tiên Authorization header (Bearer <token>), fallback sang cookie.
		// --------------------------------------------------------------------
		var rawToken string
		if bearer, ok := security.ExtractBearerToken(c.GetHeader("Authorization")); ok {
			rawToken = bearer
		} else if cookieToken, err := c.Cookie(constant.AccessTokenName); err == nil {
			rawToken = strings.TrimSpace(cookieToken)
		}
		if rawToken == "" {
			c.Header("WWW-Authenticate", "Bearer")
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}

		// --------------------------------------------------------------------
		// BƯỚC 2: LẤY SIGNING SECRET TỪ CACHE REGISTRY → PARSE JWT
		// GetOrLoad đi qua L1 in-memory trước, miss thì load từ DB.
		// --------------------------------------------------------------------
		secretVal, err := registry.GetOrLoad(c.Request.Context(), "access_secret", "")
		if err != nil {
			apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
			c.Abort()
			return
		}
		secrets, ok := secretVal.(*coreEntity.RuntimeSecrets)
		if !ok || secrets == nil {
			apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
			c.Abort()
			return
		}

		// Parse JWT lần lượt qua Active rồi Standby secret để hỗ trợ rotation.
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
		// BƯỚC 3: XÁC THỰC PHIÊN QUA REDIS L2 (SESSION BINDING)
		// access_key lấy từ claims JWT; access_secret lấy từ cookie.
		// --------------------------------------------------------------------
		accessKeyClaim := strings.TrimSpace(claims.AccessKey)
		userIDClaim := strings.TrimSpace(claims.Subject)
		jti := strings.TrimSpace(claims.TokenID)

		if accessKeyClaim == "" || userIDClaim == "" || jti == "" {
			c.Header("WWW-Authenticate", "Bearer")
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}

		// Lấy access_secret từ cookie để đối chiếu với hash trong Redis.
		accessSecretCookie, secretCookieErr := c.Cookie(constant.AccessSecretName)
		accessKeyCookie, keyCookieErr := c.Cookie(constant.AccessKeyName)
		accessSecret := strings.TrimSpace(accessSecretCookie)
		accessKeyFromCookie := strings.TrimSpace(accessKeyCookie)

		if secretCookieErr != nil || keyCookieErr != nil || accessSecret == "" || accessKeyFromCookie == "" {
			clearUserAccessCookies(c, cookieDomain, cookiePath)
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}

		// Cookie access_key phải khớp chính xác với access_key trong claims JWT.
		if accessKeyFromCookie != accessKeyClaim {
			clearUserAccessCookies(c, cookieDomain, cookiePath)
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}

		// Tra bản ghi phiên từ Redis thông qua CacheEngine L2.
		ctx, cancel := context.WithTimeout(c.Request.Context(), 100*time.Millisecond)
		defer cancel()

		sessionKey := "iam:user_access_session:" + userIDClaim + ":" + accessKeyClaim
		rawPayload, _, exists, l2Err := registry.L2.Get(ctx, sessionKey)
		if l2Err != nil {
			// Lỗi kết nối Redis → fail-closed (503).
			apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
			c.Abort()
			return
		}
		if !exists {
			// Key không tồn tại → phiên đã hết hạn hoặc bị thu hồi.
			clearUserAccessCookies(c, cookieDomain, cookiePath)
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}

		var record userAccessSession
		if jsonErr := json.Unmarshal(rawPayload, &record); jsonErr != nil {
			apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
			c.Abort()
			return
		}

		// --------------------------------------------------------------------
		// BƯỚC 4: XÁC THỰC ACCESS KEY VÀ ACCESS SECRET
		// Không cần kiểm tra JTI và trạng thái 'revoked' chi tiết ở đây vì:
		// 1. Khi logout/thu hồi phiên, bản ghi session tương ứng sẽ bị xóa hoặc vô hiệu hóa trực tiếp trong Redis.
		// 2. Việc loại bỏ kiểm tra JTI giúp tránh race condition cho các HTTP request đồng thời trong lúc xoay vòng token.
		// Chỉ cần so khớp chính xác cặp khóa (access_key và hash của access_secret).
		// --------------------------------------------------------------------
		if record.AccessSecretHash != security.HashTokenSHA256(accessSecret) {
			clearUserAccessCookies(c, cookieDomain, cookiePath)
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}

		// --------------------------------------------------------------------
		// Tiêm vào Go standard context để service layer đọc qua ctx.Value().
		goCtx := context.WithValue(c.Request.Context(), constant.ContextKeyRuntimeAccessKey, accessKeyClaim)
		goCtx = context.WithValue(goCtx, constant.ContextKeyRuntimeAccessSecret, accessSecret)
		goCtx = context.WithValue(goCtx, constant.ContextKeyUserID, claims.Subject)
		goCtx = context.WithValue(goCtx, constant.ContextKeyRole, claims.Role)
		goCtx = context.WithValue(goCtx, constant.ContextKeyJTI, claims.TokenID)
		goCtx = context.WithValue(goCtx, constant.ContextKeyLevel, claims.Level)
		goCtx = context.WithValue(goCtx, constant.ContextKeyTenant, claims.TenantID)
		if trackedDevice := strings.TrimSpace(record.TrackedDeviceID); trackedDevice != "" {
			goCtx = context.WithValue(goCtx, constant.ContextKeyTrackedDeviceID, trackedDevice)
			c.Set(constant.ContextKeyTrackedDeviceID, trackedDevice)
		}
		c.Request = c.Request.WithContext(goCtx)

		// Tiêm toàn bộ claims JWT vào Gin context.
		c.Set(constant.ContextKeyJWTClaims, claims)
		c.Set(constant.ContextKeyUserID, claims.Subject)
		c.Set(constant.ContextKeyRole, claims.Role)
		c.Set(constant.ContextKeyJTI, claims.TokenID)
		c.Set(constant.ContextKeyLevel, claims.Level)
		c.Set(constant.ContextKeyTenant, claims.TenantID)
		c.Set(constant.ContextKeyRuntimeAccessKey, accessKeyClaim)
		c.Set(constant.ContextKeyRuntimeAccessSecret, accessSecret)

		c.Next()

		// --------------------------------------------------------------------
		// CẬP NHẬT LAST SEEN (BEST-EFFORT, HAI TẦNG THROTTLE)
		// Sau khi request xử lý xong:
		//   Tầng 1 — Redis lsa: cập nhật mỗi 30s (cheap, giữ KEEPTTL).
		//   Tầng 2 — DB flush: dùng SET NX EX làm distributed gate (mỗi ~3 phút/user).
		//     Goroutine nào SET NX thành công mới được flush DB → tự rate-limit,
		//     loại bỏ write storm dù có 10k+ user online đồng thời.
		// --------------------------------------------------------------------
		now := time.Now().Unix()
		if now-record.LastSeenAt > 30 {
			// Capture IP và UA từ gin context trước khi rời khỏi request goroutine.
			clientIP := c.ClientIP()
			userAgent := c.Request.UserAgent()
			trackedDeviceID := strings.TrimSpace(record.TrackedDeviceID)

			updatedRecord := record
			updatedRecord.LastSeenAt = now
			if payload, marshalErr := json.Marshal(updatedRecord); marshalErr == nil {
				rdb := registry.L2.Client()
				// dbFlushGateKey: key tồn tại trong 3 phút, đóng vai trò distributed rate limiter cho DB write.
				dbFlushGateKey := "iam:user_lsadb:" + userIDClaim + ":" + accessKeyClaim
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
					defer cancel()

					// Tầng 1: Cập nhật lsa trong Redis, giữ nguyên TTL gốc của session.
					_ = rdb.Set(ctx, sessionKey, payload, goredis.KeepTTL).Err()

					// Tầng 2: Chỉ flush DB nếu SET NX thành công (gate chưa có ai giữ).
					// TTL 3 phút → tối đa ~1 DB write / 3 phút / session, bất kể traffic.
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

// ============================================================================
// Context Accessor Helpers
// ============================================================================

func GetUserID(c *gin.Context) string {
	return getContextString(c, constant.ContextKeyUserID)
}

func GetTrackedDeviceID(c *gin.Context) string {
	return getContextString(c, constant.ContextKeyTrackedDeviceID)
}

func GetRuntimeAccessKey(c *gin.Context) string {
	return getContextString(c, constant.ContextKeyRuntimeAccessKey)
}

func GetRuntimeAccessSecret(c *gin.Context) string {
	return getContextString(c, constant.ContextKeyRuntimeAccessSecret)
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

// clearUserAccessCookies xóa tất cả session cookies khi phiên không hợp lệ.
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
