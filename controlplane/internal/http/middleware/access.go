package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

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

var revokedJTICache = struct {
	mu        sync.RWMutex
	expiresAt map[string]time.Time
}{
	expiresAt: make(map[string]time.Time),
}

var accessMiddleware = struct {
	mu      sync.RWMutex
	handler gin.HandlerFunc
}{}

func InitAccess(sp security.SecretProvider, rdb *redis.Client,
	runtimeCache iamCache.UserDeviceRuntimeCache, graceWindow time.Duration) {
	accessMiddleware.mu.Lock()
	accessMiddleware.handler = buildAccessHandler(sp, rdb, runtimeCache, graceWindow)
	accessMiddleware.mu.Unlock()
}

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

// Access checks for a valid JWT in Authorization header or cookie.
// On success, injects identity claims and adds user_id to logger context.
func buildAccessHandler(sp security.SecretProvider, rdb *redis.Client, runtimeCache iamCache.UserDeviceRuntimeCache, graceWindow time.Duration) gin.HandlerFunc {
	if graceWindow <= 0 {
		graceWindow = 10 * time.Second
	}
	cookiePath := "/"
	cookieDomain := ""

	return func(c *gin.Context) {
		// Bước 1: lấy access token từ Authorization Bearer.
		// Không fallback cookie để giữ contract xác thực nhất quán theo header.
		token, ok := security.ExtractBearerToken(c.GetHeader("Authorization"))
		if !ok {
			c.Header("WWW-Authenticate", "Bearer")
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}

		// Bước 2: kiểm tra dependency ký/verify JWT có sẵn.
		if sp == nil {
			apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
			c.Abort()
			return
		}

		// Bước 3: lấy danh sách secret ứng viên để verify token
		// (hỗ trợ rotation key mà không downtime).
		candidates, err := sp.GetCandidates(c.Request.Context(), security.SecretFamilyAccess)
		if err != nil || len(candidates) == 0 {
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
		// Bước 4: parse/verify JWT theo từng ứng viên secret.
		for _, candidate := range candidates {
			claims, parseErr = security.Parse(token, candidate.Value)
			if parseErr == nil {
				parsed = true
				break
			}
			if errors.Is(parseErr, security.ErrEmptySecret) {
				emptySecret = true
			}
		}
		if !parsed {
			// Nếu lỗi do secret rỗng => lỗi hệ thống.
			// Ngược lại xem như token client không hợp lệ.
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

		// Bước 5: kiểm tra jti có bị revoke trong blacklist Redis hay chưa.
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

		// Bước 6 (tuỳ chọn theo runtimeCache): xác minh ràng buộc phiên device.
		// Điều kiện pass:
		// - cookie device_id/device_secret tồn tại,
		// - device_id cookie == device_id trong JWT,
		// - jti + secret khớp runtime record trong Redis tương ứng device hiện tại.
		if runtimeCache != nil {
			deviceIDCookieValue, deviceIDCookieErr := c.Cookie(constant.DeviceIDName)
			deviceSecretValue, deviceSecretErr := c.Cookie(constant.DeviceSecretName)
			deviceIDCookie := strings.TrimSpace(deviceIDCookieValue)
			deviceSecret := strings.TrimSpace(deviceSecretValue)
			deviceIDClaim := strings.TrimSpace(claims.DeviceID)
			jti := strings.TrimSpace(claims.TokenID)
			if deviceIDCookieErr != nil || deviceSecretErr != nil || deviceIDCookie == "" || deviceSecret == "" || deviceIDClaim == "" || jti == "" || strings.TrimSpace(claims.Subject) == "" {
				clearUserDeviceCookies(c, cookieDomain, cookiePath)
				apires.RespondUnauthorized(c, "unauthorized")
				c.Abort()
				return
			}
			if deviceIDCookie != deviceIDClaim {
				clearUserDeviceCookies(c, cookieDomain, cookiePath)
				apires.RespondUnauthorized(c, "unauthorized")
				c.Abort()
				return
			}

			ctx, cancel := context.WithTimeout(c.Request.Context(), 100*time.Millisecond)
			defer cancel()
			// Query model mới:
			// key chính: iam:user:device:runtime:<user_id>:<device_id>
			record, err := runtimeCache.GetDeviceRuntimeByUserDevice(ctx, claims.Subject, deviceIDClaim)
			if err != nil {
				apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
				c.Abort()
				return
			}
			if record == nil || !iamCache.MatchRuntime(record, deviceIDCookie, deviceSecret, jti, graceWindow) {
				clearUserDeviceCookies(c, cookieDomain, cookiePath)
				apires.RespondUnauthorized(c, "unauthorized")
				c.Abort()
				return
			}
			if strings.TrimSpace(record.TrackedDeviceID) != "" {
				c.Set(constant.ContextKeyTrackedDeviceID, strings.TrimSpace(record.TrackedDeviceID))
			}
		}

		// Store full claims for callers that need everything.
		c.Set(constant.ContextKeyJWTClaims, claims)

		// Inject individual identity fields as flat keys.
		c.Set(constant.ContextKeyUserID, claims.Subject)
		c.Set(constant.ContextKeyRole, claims.Role)
		c.Set(constant.ContextKeyJTI, claims.TokenID)
		c.Set(constant.ContextKeyRuntimeDeviceID, claims.DeviceID)
		c.Set(constant.ContextKeyLevel, claims.Level)
		c.Set(constant.ContextKeyTenant, claims.TenantID)

		// Bước cuối: toàn bộ kiểm tra đã pass, cho request đi tiếp.
		c.Next()
	}
}

func GetUserID(c *gin.Context) string {
	return getContextString(c, constant.ContextKeyUserID)
}

func GetTrackedDeviceID(c *gin.Context) string {
	return getContextString(c, constant.ContextKeyTrackedDeviceID)
}

func GetRuntimeDeviceID(c *gin.Context) string {
	return getContextString(c, constant.ContextKeyRuntimeDeviceID)
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

func clearUserDeviceCookies(c *gin.Context, cookieDomain, cookiePath string) {
	secure := c.Request.TLS != nil
	exp := time.Unix(0, 0)
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     constant.DeviceIDName,
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
		Name:     constant.DeviceSecretName,
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

// IsBlacklisted checks if the JTI is blacklisted in Redis.
func IsBlacklisted(ctx context.Context, rdb *redis.Client, jti string) (bool, error) {
	jti = strings.TrimSpace(jti)
	if jti == "" || rdb == nil {
		return false, nil
	}

	// Chỉ cache kết quả revoked=true. Không cache revoked=false vì logout/revoke
	// phải có hiệu lực ngay khi Redis blacklist key được ghi.
	now := time.Now().UTC()
	revokedJTICache.mu.RLock()
	cachedUntil, cachedRevoked := revokedJTICache.expiresAt[jti]
	revokedJTICache.mu.RUnlock()
	if cachedRevoked && now.Before(cachedUntil) {
		return true, nil
	}
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

	revokedJTICache.mu.Lock()
	revokedJTICache.expiresAt[jti] = now.Add(revokedJTICacheTTL)
	revokedJTICache.mu.Unlock()
	return true, nil
}
