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
)

// authServiceStub là mock service để kiểm thử hành vi của handler độc lập
type authServiceStub struct {
	err         error
	loginErr    error
	loginResult *iamEntity.LoginResult
}

var _ iamSvcInterface.AuthService = (*authServiceStub)(nil)

func (s *authServiceStub) RegisterAccount(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile, password string) error {
	return s.err
}

func (s *authServiceStub) Logout(ctx context.Context) error {
	return nil
}

func (s *authServiceStub) Login(ctx context.Context, req iamEntity.LoginRequest) (*iamEntity.LoginResult, error) {
	return s.loginResult, s.loginErr
}


func (s *authServiceStub) VerifyUserTrinitySession(ctx context.Context, token string, accessKey string, accessSecret string) (*iamEntity.VerifySessionResult, error) {
	return nil, nil
}



func newAuthHandler(service iamSvcInterface.AuthService) *handler.AuthHandler {
	cfg := config.LoadConfig()
	return handler.NewAuthHandler(cfg, service)
}

// TestRegisterAccount_BadRequest kiểm thử trường hợp dữ liệu đăng ký không hợp lệ (Bad Request)
func TestRegisterAccount_BadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := newAuthHandler(&authServiceStub{})
	router.POST("/register", h.RegisterAccount)

	// Dữ liệu đầu vào thiếu/sai định dạng email, mật khẩu không trùng khớp
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

	// Phải trả về lỗi 400 Bad Request
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// TestRegisterAccount_Conflict kiểm thử trường hợp tài khoản đã tồn tại trong hệ thống
func TestRegisterAccount_Conflict(t *testing.T) {
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

	// Phải trả về 409 Conflict khi username/email trùng
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

// TestRegisterAccount_InternalError kiểm thử lỗi hệ thống/cơ sở dữ liệu khi đăng ký tài khoản
func TestRegisterAccount_InternalError(t *testing.T) {
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

	// Phải trả về 500 Internal Server Error
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

// TestLogin_BadRequest kiểm thử trường hợp yêu cầu đăng nhập gửi dữ liệu sai định dạng
func TestLogin_BadRequest(t *testing.T) {
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

// TestLogin_InvalidCredentials kiểm thử khi thông tin đăng nhập (username/password) sai
func TestLogin_InvalidCredentials(t *testing.T) {
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

// TestLogin_VerificationRequired kiểm thử trường hợp đăng nhập đúng nhưng cần xác thực 2FA/MFA
func TestLogin_VerificationRequired(t *testing.T) {
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

// TestLogin_SuccessSetsCookies kiểm thử đăng nhập thành công và thiết lập đúng các session cookies
func TestLogin_SuccessSetsCookies(t *testing.T) {
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

// TestSession_Unauthorized kiểm thử việc truy cập session endpoint khi không có token định danh
func TestSession_Unauthorized(t *testing.T) {
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

// TestSession_Success kiểm thử lấy thông tin phiên làm việc thành công với Identity hợp lệ trong context
func TestSession_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := newAuthHandler(&authServiceStub{})
	router.GET("/session", func(c *gin.Context) {
		ident := &constant.Identity{
			UserID:    "user-1",
			AccessKey: "device-1",
		}
		ctx := context.WithValue(c.Request.Context(), constant.IdentityKey, ident)
		c.Request = c.Request.WithContext(ctx)
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

// TestSession_ServiceUnavailable kiểm thử khi thiếu middleware Access() xử lý định danh
func TestSession_ServiceUnavailable(t *testing.T) {
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

// TestSession_ReadOnly kiểm thử tính năng session endpoint là read-only (không được ghi lại/cập nhật cookies mới)
func TestSession_ReadOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := newAuthHandler(&authServiceStub{})
	router.GET("/session", func(c *gin.Context) {
		ident := &constant.Identity{
			UserID:    "user-1",
			AccessKey: "device-1",
		}
		ctx := context.WithValue(c.Request.Context(), constant.IdentityKey, ident)
		c.Request = c.Request.WithContext(ctx)
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
