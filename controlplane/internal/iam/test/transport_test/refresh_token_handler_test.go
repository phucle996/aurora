package transport_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"controlplane/internal/config"
	"controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamErrorx "controlplane/internal/iam/errorx"
	handler "controlplane/internal/iam/transport/http/handler"
	cookie "controlplane/pkg/constant"

	"github.com/gin-gonic/gin"
)

type refreshTokenServiceStub struct {
	result *iamEntity.RefreshTokenResult
	err    error
}

var _ iamSvcInterface.RefreshTokenService = (*refreshTokenServiceStub)(nil)

func (s *refreshTokenServiceStub) Refresh(ctx context.Context, rawRefreshToken string) (*iamEntity.RefreshTokenResult, error) {
	return s.result, s.err
}

func newRefreshTokenHandler(service iamSvcInterface.RefreshTokenService) *handler.RefreshTokenHandler {
	cfg := config.LoadConfig()
	return handler.NewRefreshTokenHandler(cfg, service)
}

func TestRefreshTokenHandlerMissingCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := newRefreshTokenHandler(&refreshTokenServiceStub{})
	router.POST("/refresh", h.Refresh)

	req := httptest.NewRequest(http.MethodPost, "/refresh", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestRefreshTokenHandlerInvalidSessionClearsCookies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := newRefreshTokenHandler(&refreshTokenServiceStub{err: iamErrorx.ErrInvalidSession})
	router.POST("/refresh", h.Refresh)

	req := httptest.NewRequest(http.MethodPost, "/refresh", nil)
	req.AddCookie(&http.Cookie{Name: cookie.RefreshTokenName, Value: "raw-refresh"})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	cookies := w.Result().Cookies()
	if len(cookies) < 3 {
		t.Fatalf("expected clear cookies, got %#v", cookies)
	}
}

func TestRefreshTokenHandlerAuthenticationUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := newRefreshTokenHandler(&refreshTokenServiceStub{err: iamErrorx.ErrAuthenticationUnavailable})
	router.POST("/refresh", h.Refresh)

	req := httptest.NewRequest(http.MethodPost, "/refresh", nil)
	req.AddCookie(&http.Cookie{Name: cookie.RefreshTokenName, Value: "raw-refresh"})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestRefreshTokenHandlerInternalError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := newRefreshTokenHandler(&refreshTokenServiceStub{err: errors.New("boom")})
	router.POST("/refresh", h.Refresh)

	req := httptest.NewRequest(http.MethodPost, "/refresh", nil)
	req.AddCookie(&http.Cookie{Name: cookie.RefreshTokenName, Value: "raw-refresh"})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestRefreshTokenHandlerSuccessSetsCookies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	now := time.Now().UTC()
	h := newRefreshTokenHandler(&refreshTokenServiceStub{result: &iamEntity.RefreshTokenResult{
		AccessToken:      "access-token",
		RefreshToken:     "refresh-token-next",
		RuntimeDeviceID:  "runtime-device-next",
		TrackedDeviceID:  "177682fc-3e96-4a5a-84eb-b5e9c71af721",
		AccessExpiresAt:  now.Add(15 * time.Minute),
		RefreshExpiresAt: now.Add(24 * time.Hour),
	}})
	router.POST("/refresh", h.Refresh)

	req := httptest.NewRequest(http.MethodPost, "/refresh", nil)
	req.AddCookie(&http.Cookie{Name: cookie.RefreshTokenName, Value: "raw-refresh"})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
	cookies := w.Result().Cookies()
	foundAccess := false
	foundRefresh := false
	foundDevice := false
	for _, ck := range cookies {
		if ck.Name == cookie.AccessTokenName && ck.Value == "access-token" {
			foundAccess = true
		}
		if ck.Name == cookie.RefreshTokenName && ck.Value == "refresh-token-next" {
			foundRefresh = true
		}
		if ck.Name == cookie.AccessKeyName && ck.Value == "runtime-device-next" {
			foundDevice = true
		}
	}
	if !foundAccess || !foundRefresh || !foundDevice {
		t.Fatalf("expected refreshed cookies, got %#v", cookies)
	}
}
