package middleware

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"controlplane/internal/security"
	apires "controlplane/pkg/apires"
	"controlplane/pkg/constant"

	"github.com/gin-gonic/gin"
)

const stepUpSecretTTL = 5 * time.Minute

type stepUpCacheItem struct {
	sourceKey string
	secret    string
	expiresAt time.Time
}

type AdminStepUp2FASecretLoader interface {
	Load(ctx context.Context) (cipherText string, updatedAt time.Time, err error)
}

type AdminStepUp2FASecretLoaderFunc func(ctx context.Context) (cipherText string, updatedAt time.Time, err error)

func (f AdminStepUp2FASecretLoaderFunc) Load(ctx context.Context) (cipherText string, updatedAt time.Time, err error) {
	return f(ctx)
}

var (
	stepUpCache = struct {
		mu       sync.RWMutex
		snapshot stepUpCacheItem
	}{}
	stepUpState = struct {
		mu         sync.RWMutex
		loadSecret func(ctx context.Context) (cipherText string, updatedAt time.Time, err error)
	}{}
)

// InitAdminCriticalStepUp2FA khởi tạo runtime cho critical step-up guard.
//
// Source of truth:
// - app/module.go truyền vào function load admin 2FA settings.
// - Middleware không import IAM repository/module, chỉ gọi contract function.
func InitAdminCriticalStepUp2FA(loader AdminStepUp2FASecretLoader) error {
	if loader == nil {
		return errors.New("admin critical step-up: load 2fa secret is required")
	}

	stepUpState.mu.Lock()
	stepUpState.loadSecret = loader.Load
	stepUpState.mu.Unlock()
	return nil
}

// AdminCriticalStepUp2FA enforce MFA lần 2 cho critical admin action.
//
// Contract V1:
// - chỉ chấp nhận method "totp".
// - recovery_code không được dùng cho critical action; nó chỉ thuộc login/recovery.
//
// Cache note:
//   - app/module.go cache ciphertext metadata bằng Redis TTL ngắn để giảm DB hit.
//   - Middleware chỉ cache plaintext TOTP secret sau decrypt, theo sourceKey =
//     updatedAt + hash(ciphertext). Vì vậy không có map phình theo nhiều version cũ.
func AdminCriticalStepUp2FA() gin.HandlerFunc {
	return func(c *gin.Context) {
		stepUpState.mu.RLock()
		loadSecret := stepUpState.loadSecret
		stepUpState.mu.RUnlock()
		if loadSecret == nil {
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

		secret, err := loadStepUpSecret(c.Request.Context(), loadSecret)
		if err != nil {
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

func loadStepUpSecret(
	ctx context.Context,
	loadSecret func(ctx context.Context) (cipherText string, updatedAt time.Time, err error),
) (string, error) {
	cipherText, updatedAt, err := loadSecret(ctx)
	if err != nil {
		return "", err
	}
	cipherText = strings.TrimSpace(cipherText)
	if cipherText == "" {
		return "", errors.New("admin critical step-up: totp secret is unavailable")
	}

	now := time.Now().UTC()
	cipherHash := sha256.Sum256([]byte(cipherText))
	sourceKey := fmt.Sprintf("%s:%x", updatedAt.UTC().Format(time.RFC3339Nano), cipherHash)
	stepUpCache.mu.RLock()
	cached := stepUpCache.snapshot
	stepUpCache.mu.RUnlock()
	if cached.sourceKey == sourceKey && cached.secret != "" && now.Before(cached.expiresAt) {
		return cached.secret, nil
	}

	secret, err := security.DecryptSecret(cipherText)
	if err != nil {
		return "", err
	}
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return "", errors.New("admin critical step-up: decrypted totp secret is empty")
	}

	stepUpCache.mu.Lock()
	stepUpCache.snapshot = stepUpCacheItem{
		sourceKey: sourceKey,
		secret:    secret,
		expiresAt: now.Add(stepUpSecretTTL),
	}
	stepUpCache.mu.Unlock()

	return secret, nil
}
