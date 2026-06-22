package transport_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	handler "controlplane/internal/iam/transport/http/handler"
	cookie "controlplane/pkg/constant"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type refreshTokenServiceStub struct {
	result              *iamEntity.RefreshTokenResult
	err                 error
	adminResult         iamEntity.AdminLoginResult
	adminErr            error
	adminRefreshCalled  bool
	adminZoneCode       string
}

var _ iamSvcInterface.SessionRefreshService = (*refreshTokenServiceStub)(nil)

func (s *refreshTokenServiceStub) CreateRefreshToken(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) (string, time.Time, error) {
	return "", time.Time{}, nil
}

func (s *refreshTokenServiceStub) RefreshUserOpaque(ctx context.Context, rawRefreshToken string) (*iamEntity.RefreshTokenResult, error) {
	return s.result, s.err
}



func (s *refreshTokenServiceStub) RefreshAdminTrinity(ctx context.Context, zoneCode string, ip *string, userAgent *string) (iamEntity.AdminLoginResult, error) {
	s.adminRefreshCalled = true
	s.adminZoneCode = zoneCode
	return s.adminResult, s.adminErr
}

func (s *refreshTokenServiceStub) RevokeRefreshTokensByDeviceIDAndUserID(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) error {
	return nil
}

func (s *refreshTokenServiceStub) RevokeRefreshTokensByUserID(ctx context.Context, userID uuid.UUID, exceptDeviceID *uuid.UUID) error {
	return nil
}

func (s *refreshTokenServiceStub) VerifyOpaqueRefreshToken(ctx context.Context, rawRefreshToken string, scope string) (*iamEntity.VerifyOpaqueRefreshTokenResult, error) {
	return nil, nil
}

// [COMMENT]: Thêm stub RevokeOpaqueRefreshToken để thoả mãn interface mới
func (s *refreshTokenServiceStub) RevokeOpaqueRefreshToken(ctx context.Context, rawRefreshToken string) error {
	return nil
}

func newRefreshTokenHandler(service iamSvcInterface.SessionRefreshService) *handler.RefreshTokenHandler {
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
	h := newRefreshTokenHandler(&refreshTokenServiceStub{err: iamTaxonomy.ErrInvalidSession})
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
	h := newRefreshTokenHandler(&refreshTokenServiceStub{err: iamTaxonomy.ErrAuthenticationUnavailable})
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
		AccessKey:        "runtime-device-next",
		AccessSecret:     "secret-next",
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

func TestAdminAuthHandlerRefreshMissingZoneCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := newRefreshTokenHandler(&refreshTokenServiceStub{})
	r.POST("/admin/auth/refresh", h.AdminRefresh)

	req := httptest.NewRequest(http.MethodPost, "/admin/auth/refresh", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "zone_code query parameter is required") {
		t.Fatalf("expected missing parameter error message, got: %s", w.Body.String())
	}
}

func TestAdminAuthHandlerRefreshSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	stub := &refreshTokenServiceStub{
		adminResult: iamEntity.AdminLoginResult{
			AdminAPIToken: "new-token",
			AccessKey:     "new-device-1",
			AccessSecret:  "new-secret-1",
			ExpiresAt:     time.Now().UTC().Add(10 * time.Minute),
		},
	}
	h := newRefreshTokenHandler(stub)
	r.POST("/admin/auth/refresh", h.AdminRefresh)

	req := httptest.NewRequest(http.MethodPost, "/admin/auth/refresh?zone_code=vn-hn-1", nil)
	req.AddCookie(&http.Cookie{Name: cookie.AccessKeyName, Value: "device-1"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
	if !stub.adminRefreshCalled {
		t.Fatalf("expected service refresh function to be called")
	}
	if stub.adminZoneCode != "vn-hn-1" {
		t.Fatalf("expected zoneCode propagated, got %s", stub.adminZoneCode)
	}
}

func TestAdminAuthHandlerRefreshFallbackToCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	stub := &refreshTokenServiceStub{
		adminResult: iamEntity.AdminLoginResult{
			AdminAPIToken: "new-token-2",
			AccessKey:     "new-device-2",
			AccessSecret:  "new-secret-2",
			ExpiresAt:     time.Now().UTC().Add(10 * time.Minute),
		},
	}
	h := newRefreshTokenHandler(stub)
	r.POST("/admin/auth/refresh", h.AdminRefresh)

	req := httptest.NewRequest(http.MethodPost, "/admin/auth/refresh", nil)
	req.AddCookie(&http.Cookie{Name: cookie.ZoneCodeName, Value: "vn-hn-2"})
	req.AddCookie(&http.Cookie{Name: cookie.AccessKeyName, Value: "device-2"})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d, body: %s", w.Code, w.Body.String())
	}
	if !stub.adminRefreshCalled {
		t.Fatalf("expected service refresh function to be called")
	}
	if stub.adminZoneCode != "vn-hn-2" {
		t.Fatalf("expected zoneCode resolved from cookie, got %s", stub.adminZoneCode)
	}
}

