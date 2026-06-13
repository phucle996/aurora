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
	middleware "controlplane/internal/http/middleware"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	handler "controlplane/internal/iam/transport/http/handler"
	constant "controlplane/pkg/constant"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type authServiceStub struct {
	err         error
	loginErr    error
	loginResult *iamEntity.LoginResult
}

var _ iamSvcInterface.AuthService = (*authServiceStub)(nil)

func (s *authServiceStub) RegisterAccount(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile, password string) error {
	return s.err
}

func (s *authServiceStub) Logout(ctx context.Context, userID uuid.UUID, accessKey string, accessSecret string) error {
	return nil
}

func (s *authServiceStub) Login(ctx context.Context, req iamEntity.LoginRequest) (*iamEntity.LoginResult, error) {
	return s.loginResult, s.loginErr
}

func (s *authServiceStub) VerifyAdminTrinitySession(ctx context.Context, token string, accessKey string, accessSecret string) (*iamEntity.VerifySessionResult, error) {
	return nil, nil
}

func (s *authServiceStub) VerifyUserTrinitySession(ctx context.Context, token string, accessKey string, accessSecret string) (*iamEntity.VerifySessionResult, error) {
	return nil, nil
}

func newAuthHandler(service iamSvcInterface.AuthService) *handler.AuthHandler {
	cfg := config.LoadConfig()
	return handler.NewAuthHandler(cfg, service)
}

func TestAuthHandlerRegisterAccountBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := newAuthHandler(&authServiceStub{})
	router.POST("/register", h.RegisterAccount)

	body, _ := json.Marshal(map[string]any{
		"username":    "abc",
		"email":       "bad",
		"password":    "123",
		"re_password": "456",
		"fullname":    "   ",
	})

	req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAuthHandlerRegisterAccountConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := newAuthHandler(&authServiceStub{err: iamTaxonomy.ErrUserAlreadyExist})
	router.POST("/register", h.RegisterAccount)

	body, _ := json.Marshal(map[string]any{
		"username":    "alice01",
		"email":       "user@example.com",
		"password":    "Secret123!",
		"re_password": "Secret123!",
		"fullname":    "Alice Nguyen",
	})

	req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

func TestAuthHandlerRegisterAccountInternalError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := newAuthHandler(&authServiceStub{err: errors.New("boom")})
	router.POST("/register", h.RegisterAccount)

	body, _ := json.Marshal(map[string]any{
		"username":    "alice01",
		"email":       "user@example.com",
		"password":    "Secret123!",
		"re_password": "Secret123!",
		"fullname":    "Alice Nguyen",
	})

	req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestAuthHandlerLoginBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := newAuthHandler(&authServiceStub{})
	router.POST("/login", h.Login)

	body, _ := json.Marshal(map[string]any{"username": "abc", "password": "123"})
	req := httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAuthHandlerLoginInvalidCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := newAuthHandler(&authServiceStub{loginErr: iamTaxonomy.ErrInvalidCredentials})
	router.POST("/login", h.Login)

	body, _ := json.Marshal(map[string]any{"username": "alice01", "password": "secret123", "device_public_key": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
	req := httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestAuthHandlerLoginVerificationRequired(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := newAuthHandler(&authServiceStub{loginErr: iamTaxonomy.ErrVerificationRequired})
	router.POST("/login", h.Login)

	body, _ := json.Marshal(map[string]any{"username": "alice01", "password": "secret123", "device_public_key": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
	req := httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestAuthHandlerLoginSuccessSetsCookies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := newAuthHandler(&authServiceStub{loginResult: &iamEntity.LoginResult{
		AccessToken:              "access-token",
		RefreshToken:             "refresh-token",
		AccessKey:                "runtime-device-1",
		AccessSecret:             "secret-1",
		TrackedDeviceID:          "177682fc-3e96-4a5a-84eb-b5e9c71af721",
		ClientDeviceID:           "cdid-bootstrap-1",
		ClientDeviceIDProvenance: "server-bootstrap",
		AccessExpiresAt:          time.Now().UTC().Add(15 * time.Minute),
		RefreshExpiresAt:         time.Now().UTC().Add(24 * time.Hour),
	}})
	router.POST("/login", h.Login)

	body, _ := json.Marshal(map[string]any{"username": "alice01", "password": "secret123", "device_public_key": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
	req := httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	cookies := w.Result().Cookies()
	if len(cookies) < 2 {
		t.Fatalf("expected at least 2 cookies, got %d", len(cookies))
	}
	foundAccess := false
	foundRefresh := false
	foundDevice := false
	foundClientDeviceID := false
	for _, ck := range cookies {
		switch ck.Name {
		case constant.AccessTokenName:
			foundAccess = true
		case constant.RefreshTokenName:
			foundRefresh = true
		case constant.AccessKeyName:
			if ck.Value == "runtime-device-1" {
				foundDevice = true
			}
		case constant.ClientDeviceIDName:
			if ck.Value == "cdid-bootstrap-1" {
				foundClientDeviceID = true
			}
		}
	}
	if !foundAccess || !foundRefresh || !foundDevice || !foundClientDeviceID {
		t.Fatalf("expected %s, %s, %s and %s cookies, got %#v", constant.AccessTokenName, constant.RefreshTokenName, constant.AccessKeyName, constant.ClientDeviceIDName, cookies)
	}
	if w.Result().Header.Get("X-Client-Device-Id") != "cdid-bootstrap-1" {
		t.Fatalf("expected X-Client-Device-Id header to mirror cookie, got %q", w.Result().Header.Get("X-Client-Device-Id"))
	}
}

func TestAuthHandlerSessionUnauthorizedWithoutAuthContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := newAuthHandler(&authServiceStub{})
	router.GET("/session", h.Session)

	req := httptest.NewRequest(http.MethodGet, "/session", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestAuthHandlerSessionSuccessWithAuthContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := newAuthHandler(&authServiceStub{})
	router.GET("/session", func(c *gin.Context) {
		c.Set(constant.ContextKeyUserID, "user-1")
		c.Set(constant.ContextKeyRuntimeAccessKey, "device-1")
		h.Session(c)
	})

	req := httptest.NewRequest(http.MethodGet, "/session", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got := w.Body.String(); !strings.Contains(got, `"authenticated":true`) {
		t.Fatalf("expected authenticated true payload, got %s", got)
	}
}

func TestAuthHandlerSessionServiceUnavailableWhenAccessMiddlewareMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := newAuthHandler(&authServiceStub{})
	router.GET("/session", middleware.Access(), h.Session)

	req := httptest.NewRequest(http.MethodGet, "/session", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestAuthHandlerSessionReadOnlyNoSetCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := newAuthHandler(&authServiceStub{})
	router.GET("/session", func(c *gin.Context) {
		c.Set(constant.ContextKeyUserID, "user-1")
		c.Set(constant.ContextKeyRuntimeAccessKey, "device-1")
		h.Session(c)
	})

	req := httptest.NewRequest(http.MethodGet, "/session", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got := w.Result().Cookies(); len(got) != 0 {
		t.Fatalf("session endpoint must be read-only and not set cookies, got %#v", got)
	}
}
