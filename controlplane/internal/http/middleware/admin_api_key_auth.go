// ============================================================================
// 🛡️ ARCHITECTURAL & SYSTEM CONTRACTS - ADMIN API KEY AUTH
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
//
// 💡 3. CONTEXT INJECTION (OPTION B)
//   - Di chuyển toàn bộ dữ liệu từ Gin context sang Go standard context.
//   - Nhóm toàn bộ claims (Subject, Level, ZoneID, v.v.) vào struct Identity duy nhất
//     để triệt tiêu sự phình to của context chains.
//
// ============================================================================

package middleware

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"controlplane/internal/cacheengine"
	"controlplane/internal/security"
	apires "controlplane/pkg/apires"
	"controlplane/pkg/constant"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
)

// adminAPIKeyRotationTriggerTTL là TTL (khóa chặn) của cờ yêu cầu xoay vòng khóa.
const adminAPIKeyRotationTriggerTTL = 10 * time.Minute

// adminAPIKeyAuthState giữ dependency runtime cho admin API-key middleware.
var adminAPIKeyAuthState = struct {
	mu       sync.RWMutex
	registry *cacheengine.CacheRegistry
}{}

type adminAuthOptions struct {
	injectAccessKey    bool
	injectAccessSecret bool
	injectTokenJTI     bool
}

// AdminAuthOption điều khiển dữ liệu nào được inject vào Identity struct.
type AdminAuthOption func(*adminAuthOptions)

// InitAdminAPIKeyAuth khởi tạo dependency cho AdminAPIKeyAuth.
func InitAdminAPIKeyAuth(registry *cacheengine.CacheRegistry) error {
	if registry == nil {
		return errors.New("admin api key auth: cache registry is required")
	}

	adminAPIKeyAuthState.mu.Lock()
	adminAPIKeyAuthState.registry = registry
	adminAPIKeyAuthState.mu.Unlock()
	return nil
}

// WithInjectAdminAccessKey chỉ định middleware tiêm access_key của Admin vào Identity context struct (no-op).
func WithInjectAdminAccessKey() AdminAuthOption {
	return func(options *adminAuthOptions) {
		if options != nil {
			options.injectAccessKey = true
		}
	}
}

// WithInjectAdminAccessSecret chỉ định middleware tiêm access_secret của Admin vào Identity context struct (no-op).
func WithInjectAdminAccessSecret() AdminAuthOption {
	return func(options *adminAuthOptions) {
		if options != nil {
			options.injectAccessSecret = true
		}
	}
}

// AdminAPIKeyAuth xác thực admin runtime session bằng API key gửi từ Client.
func AdminAPIKeyAuth(opts ...AdminAuthOption) gin.HandlerFunc {
	options := adminAuthOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	return func(c *gin.Context) {
		adminAPIKeyAuthState.mu.RLock()
		registry := adminAPIKeyAuthState.registry
		adminAPIKeyAuthState.mu.RUnlock()
		if registry == nil {
			abortAdminAuthUnavailable(c)
			return
		}

		// 1. Trích xuất API Key từ X-Admin-API-Key hoặc Authorization Header
		apiKey := strings.TrimSpace(c.GetHeader("X-Admin-API-Key"))
		if apiKey == "" {
			authHeader := c.GetHeader("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				apiKey = strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
			} else {
				apiKey = strings.TrimSpace(authHeader)
			}
		}

		if apiKey == "" {
			logger.HandlerWarn(c, "admin.auth.header", nil, "missing or empty admin API key")
			abortAdminUnauthorized(c)
			return
		}

		// 2. Load active API Key Hash từ L1 Cache
		val, err := registry.GetOrLoad(c.Request.Context(), "admin_api_key_active", "")
		if err != nil {
			logger.HandlerError(c, "admin.auth.cache", err)
			abortAdminAuthUnavailable(c)
			return
		}

		activeHash, ok := val.(string)
		if !ok || activeHash == "" {
			logger.HandlerError(c, "admin.auth.cache", fmt.Errorf("invalid active api key format in cache"))
			abortAdminAuthUnavailable(c)
			return
		}

		// 3. So sánh Hash
		incomingHash := security.HashTokenSHA256(apiKey)
		if incomingHash != activeHash {
			logger.HandlerWarn(c, "admin.auth.verify", nil, "invalid admin API key")
			abortAdminUnauthorized(c)
			return
		}

		// 4. Inject Identity với mức tối cao (supreme)
		ident := &constant.Identity{
			UserID: "admin",
			Role:   "admin",
			Level:  99,
			ZoneID: "global",
		}

		if options.injectAccessKey {
			ident.AccessKey = "admin_key"
		}
		if options.injectAccessSecret {
			ident.AccessSecret = apiKey
		}

		// Ghi nhận Identity vào Go standard context
		goCtx := context.WithValue(c.Request.Context(), constant.IdentityKey, ident)
		c.Request = c.Request.WithContext(goCtx)

		c.Next()
	}
}

func abortAdminUnauthorized(c *gin.Context) {
	apires.RespondUnauthorized(c, "unauthorized")
	c.Abort()
}

func abortAdminAuthUnavailable(c *gin.Context) {
	apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
	c.Abort()
}
