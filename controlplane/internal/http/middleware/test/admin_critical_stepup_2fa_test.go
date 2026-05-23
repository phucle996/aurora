package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
	if err := middleware.InitAdminCriticalStepUp2FA(middleware.AdminStepUp2FASecretLoaderFunc(func(context.Context) (string, time.Time, error) {
		return totpSecret.ciphertext, totpSecret.updatedAt, nil
	})); err != nil {
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
	validReq.Header.Set(constant.HeaderAdminStepUpMethod, "totp")
	validReq.Header.Set(constant.HeaderAdminStepUpCode, code)
	validRec := httptest.NewRecorder()
	router.ServeHTTP(validRec, validReq)
	if validRec.Code != http.StatusNoContent {
		t.Fatalf("valid step-up status = %d, want %d", validRec.Code, http.StatusNoContent)
	}

	recoveryReq := httptest.NewRequest(http.MethodPost, "/admin/critical", nil)
	recoveryReq.Header.Set(constant.HeaderAdminStepUpMethod, "recovery_code")
	recoveryReq.Header.Set(constant.HeaderAdminStepUpCode, code)
	recoveryRec := httptest.NewRecorder()
	router.ServeHTTP(recoveryRec, recoveryReq)
	if recoveryRec.Code != http.StatusUnauthorized {
		t.Fatalf("recovery code status = %d, want %d", recoveryRec.Code, http.StatusUnauthorized)
	}
}

func TestAdminCriticalStepUp2FAMissingSecretUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreRuntimeMasterKey(t)

	if err := middleware.InitAdminCriticalStepUp2FA(middleware.AdminStepUp2FASecretLoaderFunc(func(context.Context) (string, time.Time, error) {
		return "", time.Time{}, nil
	})); err != nil {
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
	req.Header.Set(constant.HeaderAdminStepUpMethod, "totp")
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
	if err := middleware.InitAdminCriticalStepUp2FA(middleware.AdminStepUp2FASecretLoaderFunc(func(context.Context) (string, time.Time, error) {
		loadCalls++
		return "should-not-load", time.Now().UTC(), nil
	})); err != nil {
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
	req.Header.Set(constant.HeaderAdminStepUpMethod, "totp")
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

func TestLoadStepUpTOTPSecretDoesNotUseStaleCacheWhenCiphertextChanges(t *testing.T) {
	restoreRuntimeMasterKey(t)

	first := newEncryptedTOTPSecret(t)
	second := newEncryptedTOTPSecret(t)
	updatedAt := time.Now().UTC()

	currentCiphertext := first.ciphertext
	if err := middleware.InitAdminCriticalStepUp2FA(middleware.AdminStepUp2FASecretLoaderFunc(func(context.Context) (string, time.Time, error) {
		return currentCiphertext, updatedAt, nil
	})); err != nil {
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
	firstReq.Header.Set(constant.HeaderAdminStepUpMethod, "totp")
	firstReq.Header.Set(constant.HeaderAdminStepUpCode, firstCode)
	firstRec := httptest.NewRecorder()
	router.ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusNoContent {
		t.Fatalf("first code status = %d, want %d", firstRec.Code, http.StatusNoContent)
	}

	currentCiphertext = second.ciphertext
	secondCode, err := totp.GenerateCode(second.plain, time.Now())
	if err != nil {
		t.Fatalf("generate second code: %v", err)
	}
	secondReq := httptest.NewRequest(http.MethodPost, "/admin/critical", nil)
	secondReq.Header.Set(constant.HeaderAdminStepUpMethod, "totp")
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
