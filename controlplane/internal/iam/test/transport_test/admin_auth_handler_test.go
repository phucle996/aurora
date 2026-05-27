package transport_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvc "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	handler "controlplane/internal/iam/transport/http/handler"
	"controlplane/pkg/apperr"
	cookie "controlplane/pkg/constant"

	"github.com/gin-gonic/gin"
)

type adminAuthServiceStub struct {
	loginFn   func(ctx context.Context, req iamEntity.AdminLoginRequest) (iamEntity.AdminLoginResult, error)
	refreshFn func(ctx context.Context, deviceID string, ip *string, userAgent *string) (iamEntity.AdminLoginResult, error)
	logoutFn  func(ctx context.Context, deviceID string, ip *string, userAgent *string) error
	rotateFn  func(ctx context.Context, actor string) error
}

var _ iamSvc.AdminAPIKeyService = (*adminAuthServiceStub)(nil)

func (s *adminAuthServiceStub) Bootstrap(ctx context.Context, actor string) error { return nil }
func (s *adminAuthServiceStub) AdminLogin(ctx context.Context, req iamEntity.AdminLoginRequest) (iamEntity.AdminLoginResult, error) {
	if s.loginFn != nil {
		return s.loginFn(ctx, req)
	}
	return iamEntity.AdminLoginResult{}, nil
}
func (s *adminAuthServiceStub) AdminLogout(ctx context.Context, deviceID string, ip *string, userAgent *string) error {
	if s.logoutFn != nil {
		return s.logoutFn(ctx, deviceID, ip, userAgent)
	}
	return nil
}
func (s *adminAuthServiceStub) RefreshAdminSession(ctx context.Context, deviceID string, ip *string, userAgent *string) (iamEntity.AdminLoginResult, error) {
	if s.refreshFn != nil {
		return s.refreshFn(ctx, deviceID, ip, userAgent)
	}
	return iamEntity.AdminLoginResult{}, nil
}
func (s *adminAuthServiceStub) RotateAdminAPIKeyEmergency(ctx context.Context, actor string) error {
	if s.rotateFn != nil {
		return s.rotateFn(ctx, actor)
	}
	return nil
}
func (s *adminAuthServiceStub) TryProcessAdminKeyRotationTrigger(ctx context.Context) error {
	return nil
}
func (s *adminAuthServiceStub) FinalizeInactiveSessions(ctx context.Context, inactiveBefore time.Time, limit int) error {
	return nil
}

func newAdminAuthHandler(svc iamSvc.AdminAPIKeyService) *handler.AdminAuthHandler {
	cfg := config.LoadConfig()
	cfg.App.PublicDomain = ""
	return handler.NewAdminAuthHandler(cfg, svc)
}

func TestAdminAuthHandlerLoginSuccessSetsThreeCookies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := newAdminAuthHandler(&adminAuthServiceStub{loginFn: func(ctx context.Context, req iamEntity.AdminLoginRequest) (iamEntity.AdminLoginResult, error) {
		return iamEntity.AdminLoginResult{
			AdminAPIToken: "admin-token",
			DeviceID:      "device-1",
			DeviceSecret:  "secret-1",
			ExpiresAt:     time.Now().UTC().Add(10 * time.Minute),
		}, nil
	}})
	r.POST("/admin/auth/login", h.Login)

	body, _ := json.Marshal(map[string]any{
		"admin_api_key":     "admin-key-12345678",
		"mfa_method":        "totp",
		"mfa_code":          "123456",
		"device_public_key": "public-key-raw-12345678",
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
	cookies := w.Result().Cookies()
	m := map[string]*http.Cookie{}
	for _, ck := range cookies {
		m[ck.Name] = ck
	}
	if m[cookie.AdminAPITokenName] == nil || m[cookie.AccessKeyName] == nil || m[cookie.AccessSecretName] == nil {
		t.Fatalf("expected 3 cookies admin_api_token/access_key/access_secret")
	}
	if !m[cookie.AdminAPITokenName].HttpOnly {
		t.Fatalf("admin_api_token must be HttpOnly")
	}
	if !m[cookie.AccessSecretName].HttpOnly {
		t.Fatalf("access_secret must be HttpOnly")
	}
}

func TestAdminAuthHandlerLoginUnauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := newAdminAuthHandler(&adminAuthServiceStub{loginFn: func(ctx context.Context, req iamEntity.AdminLoginRequest) (iamEntity.AdminLoginResult, error) {
		return iamEntity.AdminLoginResult{}, iamTaxonomy.ErrAdminLoginMFAInvalid
	}})
	r.POST("/admin/auth/login", h.Login)

	body, _ := json.Marshal(map[string]any{
		"admin_api_key":     "admin-key-12345678",
		"mfa_method":        "totp",
		"mfa_code":          "123456",
		"device_public_key": "public-key-raw-12345678",
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 got %d", w.Code)
	}
}

func TestAdminAuthHandlerLoginUnauthorizedWithAppError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := newAdminAuthHandler(&adminAuthServiceStub{loginFn: func(ctx context.Context, req iamEntity.AdminLoginRequest) (iamEntity.AdminLoginResult, error) {
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAdminLoginMFAInvalid, errors.New("totp mismatch"), iamTaxonomy.AdminLoginOutcomeMFAInvalid)
	}})
	r.POST("/admin/auth/login", h.Login)

	body, _ := json.Marshal(map[string]any{
		"admin_api_key":     "admin-key-12345678",
		"mfa_method":        "totp",
		"mfa_code":          "123456",
		"device_public_key": "public-key-raw-12345678",
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "unauthorized") {
		t.Fatalf("expected generic unauthorized response")
	}
	if strings.Contains(w.Body.String(), "totp mismatch") {
		t.Fatalf("response must not leak cause")
	}
}

func TestAdminAuthHandlerLoginInternalWithAppError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := newAdminAuthHandler(&adminAuthServiceStub{loginFn: func(ctx context.Context, req iamEntity.AdminLoginRequest) (iamEntity.AdminLoginResult, error) {
		return iamEntity.AdminLoginResult{}, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, errors.New("db timeout"), iamTaxonomy.AdminLoginOutcomeAuthUnavailable)
	}})
	r.POST("/admin/auth/login", h.Login)

	body, _ := json.Marshal(map[string]any{
		"admin_api_key":     "admin-key-12345678",
		"mfa_method":        "totp",
		"mfa_code":          "123456",
		"device_public_key": "public-key-raw-12345678",
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "internal_error") {
		t.Fatalf("expected generic internal_error response")
	}
	if strings.Contains(w.Body.String(), "db timeout") {
		t.Fatalf("response must not leak cause")
	}
}

func TestAdminAuthHandlerLogoutClearsThreeCookies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := newAdminAuthHandler(&adminAuthServiceStub{logoutFn: func(ctx context.Context, deviceID string, ip *string, userAgent *string) error {
		if deviceID != "device-1" {
			t.Fatalf("expected device id propagated")
		}
		if ip == nil || strings.TrimSpace(*ip) == "" {
			t.Fatalf("expected request ip propagated")
		}
		if userAgent == nil || strings.TrimSpace(*userAgent) == "" {
			t.Fatalf("expected user-agent propagated")
		}
		return nil
	}})
	r.POST("/admin/auth/logout", h.Logout)

	req := httptest.NewRequest(http.MethodPost, "/admin/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: cookie.AccessKeyName, Value: "device-1"})
	req.Header.Set("User-Agent", "transport-test-agent")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 got %d", w.Code)
	}
	cookies := w.Result().Cookies()
	m := map[string]*http.Cookie{}
	for _, ck := range cookies {
		m[ck.Name] = ck
	}
	if m[cookie.AdminAPITokenName] == nil || m[cookie.AccessKeyName] == nil || m[cookie.AccessSecretName] == nil {
		t.Fatalf("expected clear 3 cookies")
	}
	if m[cookie.AdminAPITokenName].MaxAge != -1 || m[cookie.AccessKeyName].MaxAge != -1 || m[cookie.AccessSecretName].MaxAge != -1 {
		t.Fatalf("expected cookies max-age -1 on logout")
	}
}

func TestAdminAuthHandlerLogoutInternalError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := newAdminAuthHandler(&adminAuthServiceStub{logoutFn: func(ctx context.Context, deviceID string, ip *string, userAgent *string) error {
		return errors.New("boom")
	}})
	r.POST("/admin/auth/logout", h.Logout)

	req := httptest.NewRequest(http.MethodPost, "/admin/auth/logout", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 got %d", w.Code)
	}
}

func TestAdminAuthHandlerRotateKeyLockBusyUnauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := newAdminAuthHandler(&adminAuthServiceStub{rotateFn: func(ctx context.Context, actor string) error {
		return iamTaxonomy.ErrAdminRotationLockBusy
	}})
	r.POST("/admin/auth/rotate-key", h.RotateKey)

	req := httptest.NewRequest(http.MethodPost, "/admin/auth/rotate-key", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 got %d", w.Code)
	}
}

func TestAdminAuthHandlerSessionAuthenticated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := newAdminAuthHandler(&adminAuthServiceStub{})
	r.GET("/admin/auth/session", h.Session)

	req := httptest.NewRequest(http.MethodGet, "/admin/auth/session", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}

	var payload struct {
		Data struct {
			Authenticated bool `json:"authenticated"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if !payload.Data.Authenticated {
		t.Fatalf("expected authenticated=true")
	}
}
