package middleware

import (
	"errors"
	"strings"
	"sync"

	"controlplane/internal/cacheengine"
	"controlplane/internal/security"
	apires "controlplane/pkg/apires"
	"controlplane/pkg/constant"

	"github.com/gin-gonic/gin"
)

var stepUpState = struct {
	mu          sync.RWMutex
	cacheEngine *cacheengine.CacheRegistry
}{}

// InitAdminCriticalStepUp2FA khởi tạo runtime cho middleware Step-Up 2FA.
func InitAdminCriticalStepUp2FA(cacheEngine *cacheengine.CacheRegistry) error {
	if cacheEngine == nil {
		return errors.New("admin critical step-up: cache engine is required")
	}

	stepUpState.mu.Lock()
	stepUpState.cacheEngine = cacheEngine
	stepUpState.mu.Unlock()
	return nil
}

// AdminCriticalStepUp2FA yêu cầu xác thực yếu tố thứ 2 (MFA) tức thời đối với các hành động admin quan trọng.
func AdminCriticalStepUp2FA() gin.HandlerFunc {
	return func(c *gin.Context) {
		stepUpState.mu.RLock()
		cacheEngine := stepUpState.cacheEngine
		stepUpState.mu.RUnlock()
		if cacheEngine == nil {
			apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
			c.Abort()
			return
		}

		code := strings.TrimSpace(c.GetHeader(constant.HeaderAdminStepUpCode))
		if code == "" {
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}

		if len(code) != 6 {
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}
		for _, ch := range code {
			if ch < '0' || ch > '9' {
				apires.RespondUnauthorized(c, "unauthorized")
				c.Abort()
				return
			}
		}

		val, err := cacheEngine.GetOrLoad(c.Request.Context(), "admin_2fa_secret", "")
		if err != nil || val == nil {
			apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
			c.Abort()
			return
		}
		secret, ok := val.(string)
		if !ok || secret == "" {
			apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
			c.Abort()
			return
		}

		if !security.ValidateTOTP(code, secret) {
			apires.RespondUnauthorized(c, "unauthorized")
			c.Abort()
			return
		}

		c.Next()
	}
}
