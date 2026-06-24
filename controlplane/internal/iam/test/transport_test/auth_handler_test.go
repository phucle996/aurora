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

	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	coreEntity "controlplane/internal/core/domain/entity"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	handler "controlplane/internal/iam/transport/http/handler"
	constant "controlplane/pkg/constant"

	"github.com/gin-gonic/gin"
)

// authServiceStub là mock service để kiểm thử hành vi của handler độc lập
type authServiceStub struct {
	err      error
	loginErr error
}

var _ iamSvcInterface.AuthService = (*authServiceStub)(nil)

func (s *authServiceStub) RegisterAccount(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile, password string) error {
	return s.err
}

// [COMMENT]: Thêm stub RevokeOpaqueRefreshToken để thoả mãn interface mới
func (s *authServiceStub) RevokeOpaqueRefreshToken(ctx context.Context, rawRefreshToken string) error {
	return nil
}

// [COMMENT]: Thêm stub VerifyUserCredentials để thoả mãn interface AuthService
func (s *authServiceStub) VerifyUserCredentials(ctx context.Context, req iamEntity.LoginRequest) (*iamEntity.VerifyUserCredentialsResult, error) {
	if s.loginErr != nil {
		return nil, s.loginErr
	}
	return &iamEntity.VerifyUserCredentialsResult{
		Valid:  true,
		UserID: "test-user-id",
	}, nil
}

func (s *authServiceStub) VerifyOpaqueRefreshToken(ctx context.Context, refreshToken string, scope string) (*iamEntity.VerifyOpaqueRefreshTokenResult, error) {
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

// TestSession_Unauthorized kiểm thử việc truy cập session endpoint khi không có token định danh
func TestSession_Unauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := newAuthHandler(&authServiceStub{})

	registry := cacheengine.NewCacheRegistry(cacheengine.NewL1Cache())
	cacheengine.Register(registry, "access_secret", 1*time.Hour, func(ctx context.Context, param string) (*coreEntity.RuntimeSecrets, error) {
		return &coreEntity.RuntimeSecrets{
			Active: coreEntity.RuntimeSecret{Secret: []byte("some-test-secret-12345678901234567890123456789012")},
		}, nil
	})

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
	req.Header.Set("x-user-id", "user-1")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got := w.Body.String(); !strings.Contains(got, `"authenticated":true`) {
		t.Fatalf("expected authenticated true payload, got %s", got)
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
	req.Header.Set("x-user-id", "user-1")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got := w.Result().Cookies(); len(got) != 0 {
		t.Fatalf("session endpoint must be read-only and not set cookies, got %#v", got)
	}
}
