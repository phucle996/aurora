package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"controlplane/internal/cacheengine"
	"controlplane/internal/http/middleware"
	"controlplane/internal/security"
	"controlplane/pkg/constant"

	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"
)

func TestAdminCriticalStepUp2FA(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreRuntimeMasterKey(t)

	totpSecret := newEncryptedTOTPSecret(t)
	registry := cacheengine.NewCacheRegistry(cacheengine.NewL1Cache())
	cacheengine.Register(registry, "admin_2fa_secret", time.Hour, func(ctx context.Context, param string) (string, error) {
		return totpSecret.plain, nil
	})

	if err := middleware.InitAdminCriticalStepUp2FA(registry); err != nil {
		t.Fatalf("init step-up guard: %v", err)
	}

	router := gin.New()
	router.POST("/admin/critical",
		middleware.AdminCriticalStepUp2FA(),
		func(c *gin.Context) {
			c.Status(http.StatusNoContent)
		},
	)

	code, err := totp.GenerateCode(totpSecret.plain, time.Now())
	if err != nil {
		t.Fatalf("generate totp code: %v", err)
	}
	validReq := httptest.NewRequest(http.MethodPost, "/admin/critical", nil)
	validReq.Header.Set(constant.HeaderAdminStepUpCode, code)
	validRec := httptest.NewRecorder()
	router.ServeHTTP(validRec, validReq)
	if validRec.Code != http.StatusNoContent {
		t.Fatalf("valid step-up status = %d, want %d", validRec.Code, http.StatusNoContent)
	}
}

func TestAdminCriticalStepUp2FAMissingSecretUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreRuntimeMasterKey(t)

	registry := cacheengine.NewCacheRegistry(cacheengine.NewL1Cache())
	cacheengine.Register(registry, "admin_2fa_secret", time.Hour, func(ctx context.Context, param string) (string, error) {
		return "", nil
	})

	if err := middleware.InitAdminCriticalStepUp2FA(registry); err != nil {
		t.Fatalf("init step-up guard: %v", err)
	}

	router := gin.New()
	router.POST("/admin/critical",
		middleware.AdminCriticalStepUp2FA(),
		func(c *gin.Context) {
			c.Status(http.StatusNoContent)
		},
	)

	req := httptest.NewRequest(http.MethodPost, "/admin/critical", nil)
	req.Header.Set(constant.HeaderAdminStepUpCode, "123456")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing secret status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestAdminCriticalStepUp2FARejectsInvalidCodeBeforeLoadingSecret(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreRuntimeMasterKey(t)

	loadCalls := 0
	registry := cacheengine.NewCacheRegistry(cacheengine.NewL1Cache())
	cacheengine.Register(registry, "admin_2fa_secret", time.Hour, func(ctx context.Context, param string) (string, error) {
		loadCalls++
		return "should-not-load", nil
	})

	if err := middleware.InitAdminCriticalStepUp2FA(registry); err != nil {
		t.Fatalf("init step-up guard: %v", err)
	}

	router := gin.New()
	router.POST("/admin/critical",
		middleware.AdminCriticalStepUp2FA(),
		func(c *gin.Context) {
			c.Status(http.StatusNoContent)
		},
	)

	req := httptest.NewRequest(http.MethodPost, "/admin/critical", nil)
	req.Header.Set(constant.HeaderAdminStepUpCode, "not-a-code")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid code status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if loadCalls != 0 {
		t.Fatalf("secret loader calls = %d, want 0", loadCalls)
	}
}

func TestLoadStepUpTOTPSecretDoesNotUseStaleCacheWhenDeleted(t *testing.T) {
	restoreRuntimeMasterKey(t)

	first := newEncryptedTOTPSecret(t)
	second := newEncryptedTOTPSecret(t)

	currentSecret := first.plain
	registry := cacheengine.NewCacheRegistry(cacheengine.NewL1Cache())
	cacheengine.Register(registry, "admin_2fa_secret", time.Hour, func(ctx context.Context, param string) (string, error) {
		return currentSecret, nil
	})

	if err := middleware.InitAdminCriticalStepUp2FA(registry); err != nil {
		t.Fatalf("init step-up guard: %v", err)
	}

	router := gin.New()
	router.POST("/admin/critical",
		middleware.AdminCriticalStepUp2FA(),
		func(c *gin.Context) {
			c.Status(http.StatusNoContent)
		},
	)

	firstCode, err := totp.GenerateCode(first.plain, time.Now())
	if err != nil {
		t.Fatalf("generate first code: %v", err)
	}
	firstReq := httptest.NewRequest(http.MethodPost, "/admin/critical", nil)
	firstReq.Header.Set(constant.HeaderAdminStepUpCode, firstCode)
	firstRec := httptest.NewRecorder()
	router.ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusNoContent {
		t.Fatalf("first code status = %d, want %d", firstRec.Code, http.StatusNoContent)
	}

	// Change secret, invalidate/delete from L1 cache
	currentSecret = second.plain
	registry.L1.Delete("admin_2fa_secret")

	secondCode, err := totp.GenerateCode(second.plain, time.Now())
	if err != nil {
		t.Fatalf("generate second code: %v", err)
	}
	secondReq := httptest.NewRequest(http.MethodPost, "/admin/critical", nil)
	secondReq.Header.Set(constant.HeaderAdminStepUpCode, secondCode)
	secondRec := httptest.NewRecorder()
	router.ServeHTTP(secondRec, secondReq)
	if secondRec.Code != http.StatusNoContent {
		t.Fatalf("second code status = %d, want %d", secondRec.Code, http.StatusNoContent)
	}
}

type encryptedTOTPSecret struct {
	plain      string
	ciphertext string
	updatedAt  time.Time
}

func newEncryptedTOTPSecret(t *testing.T) encryptedTOTPSecret {
	t.Helper()

	generated, err := security.GenerateTOTP("controlplane-test", "admin")
	if err != nil {
		t.Fatalf("generate totp secret: %v", err)
	}
	ciphertext, err := security.EncryptSecret(generated.Secret)
	if err != nil {
		t.Fatalf("encrypt totp secret: %v", err)
	}
	return encryptedTOTPSecret{
		plain:      generated.Secret,
		ciphertext: ciphertext,
		updatedAt:  time.Now().UTC(),
	}
}

func restoreRuntimeMasterKey(t *testing.T) {
	t.Helper()

	previous := security.GetRuntimeMasterKey()
	security.SetRuntimeMasterKey([]byte("12345678901234567890123456789012"))
	t.Cleanup(func() {
		security.SetRuntimeMasterKey(previous)
	})
}
