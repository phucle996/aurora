package transport_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	handler "controlplane/internal/iam/transport/http/handler"

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

// Stop thoả mãn interface AuthService
func (s *authServiceStub) Stop() {}

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
