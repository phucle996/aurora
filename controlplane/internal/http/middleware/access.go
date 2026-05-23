package middleware

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

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

// Context keys injected by the Access middleware.
// Use these constants in handlers instead of string literals.
const (
	jwtClaimsContextKey = "jwt_claims" // full security.Claims object

	CtxKeyUserID = "user_id" // string — JWT subject
	CtxKeyRole   = "role"    // string — user role
	CtxKeyJTI    = "jti"     // string — token ID
	CtxKeyStatus = "status"  // string — account status
	CtxKeyLevel  = "level"   // int    — security level (0=highest)
	CtxKeyTenant = "tenant"  // string — tenant ID
	// CtxKeyRuntimeDeviceID is the token-fragment device id. It comes from
	// JWT claim device_id and must match cookie device_id.
	CtxKeyRuntimeDeviceID = "runtime_device_id"
	// CtxKeyTrackedDeviceID is the persistent DB device id (iam.devices.id).
	// Device-management services must use this value, not the runtime id.
	CtxKeyTrackedDeviceID = "tracked_device_id"
	// CtxKeyTrackingID là id stable của phiên runtime/thiết bị dài hạn,
	// dùng để index Redis user device runtime cache.
	CtxKeyTrackingID = "tracking_id"

	// Deprecated: use CtxKeyRuntimeDeviceID or CtxKeyTrackedDeviceID explicitly.
	CtxKeyDeviceID = CtxKeyRuntimeDeviceID
)

// Access checks for a valid JWT in Authorization header or cookie.
// On success, injects identity claims and adds user_id to logger context.
func Access(sp security.SecretProvider, rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := security.ExtractBearerToken(c.GetHeader("Authorization"))
		if !ok {
			cookieToken, err := c.Cookie(constant.AccessTokenName)
			cookieToken = strings.TrimSpace(cookieToken)
			if err != nil || cookieToken == "" {
				c.Header("WWW-Authenticate", "Bearer")
				apires.RespondUnauthorized(c, "unauthorized")
				c.Abort()
				return
			}
			token = cookieToken
		}

		if sp == nil {
			apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
			c.Abort()
			return
		}

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

		// Store full claims for callers that need everything.
		c.Set(jwtClaimsContextKey, claims)

		// Inject individual identity fields as flat keys.
		c.Set(CtxKeyUserID, claims.Subject)
		c.Set(CtxKeyRole, claims.Role)
		c.Set(CtxKeyJTI, claims.TokenID)
		c.Set(CtxKeyStatus, claims.Status)
		c.Set(CtxKeyLevel, claims.Level)
		c.Set(CtxKeyRuntimeDeviceID, claims.DeviceID)
		c.Set(CtxKeyTrackingID, claims.TrackingID)
		c.Set(CtxKeyTenant, claims.TenantID)

		// Piggyback on logger key so request logs include user_id automatically.
		if claims.Subject != "" {
			c.Set(logger.KeyUserID, claims.Subject)
		}

		c.Next()
	}
}

func GetUserID(c *gin.Context) string {
	v, ok := c.Get(CtxKeyUserID)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func GetRuntimeDeviceID(c *gin.Context) string {
	v, ok := c.Get(CtxKeyRuntimeDeviceID)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func GetTrackedDeviceID(c *gin.Context) string {
	v, ok := c.Get(CtxKeyTrackedDeviceID)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// GetTrackingID trả về id stable phiên runtime của device đã được Access()
// inject vào context. Dùng để lookup Redis user device runtime cache.
func GetTrackingID(c *gin.Context) string {
	v, ok := c.Get(CtxKeyTrackingID)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// Deprecated: use GetRuntimeDeviceID or GetTrackedDeviceID explicitly.
func GetDeviceID(c *gin.Context) string {
	return GetRuntimeDeviceID(c)
}

// RequireDeviceID xác thực runtime device binding của user access session.
//
// Middleware này thuộc access flow vì device_id là session fragment đi kèm
// access token: cookie device_id phải khớp với claim device_id đã được Access()
// parse và inject vào gin.Context.
func RequireDeviceID() gin.HandlerFunc {
	return func(c *gin.Context) {
		deviceIDCookie, err := c.Cookie(constant.DeviceIDName)
		deviceIDCookie = strings.TrimSpace(deviceIDCookie)
		if err != nil || deviceIDCookie == "" {
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}

		deviceIDClaim := strings.TrimSpace(GetRuntimeDeviceID(c))
		if deviceIDClaim == "" {
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}

		if deviceIDCookie != deviceIDClaim {
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}
		c.Next()
	}
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
