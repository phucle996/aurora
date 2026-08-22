package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestPersonalAuthorizationMiddlewareRequiresBothRedisBoundaries(t *testing.T) {
	if _, err := NewPersonalAuthorizationMiddleware(nil, nil); err == nil {
		t.Fatal("expected middleware construction to reject missing Redis boundaries")
	}
}

func TestPersonalAuthorizationMiddlewareAuthorizesPlatformL1Hit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	authorization := &PersonalAuthorizationMiddleware{l1: map[uuid.UUID]personalAuthorizationCacheEntry{
		userID: {permissions: map[string]struct{}{"billing:pricing_schedule:read": {}}, expiresAt: time.Now().Add(time.Minute)},
	}}
	router := gin.New()
	router.Use(ContextInjector())
	router.GET("/pricing-schedules", authorization.Authorize("billing:pricing_schedule:read", false), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	request := httptest.NewRequest(http.MethodGet, "/pricing-schedules", nil)
	request.Header.Set("x-user-id", userID.String())
	request.Header.Set("x-user-name", "billing_admin")
	request.Header.Set("x-zone-id", "019f3d3e-997d-7894-9236-c5122634cb4f")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("expected authorized response, got %d: %s", response.Code, response.Body.String())
	}
}

func TestPersonalAuthorizationMiddlewareRejectsMissingPlatformPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	authorization := &PersonalAuthorizationMiddleware{l1: map[uuid.UUID]personalAuthorizationCacheEntry{
		userID: {permissions: map[string]struct{}{}, expiresAt: time.Now().Add(time.Minute)},
	}}
	router := gin.New()
	router.Use(ContextInjector())
	router.GET("/pricing-schedules", authorization.Authorize("billing:pricing_schedule:read", false), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	request := httptest.NewRequest(http.MethodGet, "/pricing-schedules", nil)
	request.Header.Set("x-user-id", userID.String())
	request.Header.Set("x-user-name", "billing_admin")
	request.Header.Set("x-zone-id", "019f3d3e-997d-7894-9236-c5122634cb4f")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected missing permission to be rejected, got %d", response.Code)
	}
}

func TestPersonalAuthorizationMiddlewareTreatsPlatformTenantAsPlatformScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	authorization := &PersonalAuthorizationMiddleware{l1: map[uuid.UUID]personalAuthorizationCacheEntry{
		userID: {permissions: map[string]struct{}{"billing:pricing_schedule:read": {}}, expiresAt: time.Now().Add(time.Minute)},
	}}
	router := gin.New()
	router.Use(ContextInjector())
	router.GET("/pricing-schedules", authorization.Authorize("billing:pricing_schedule:read", false), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	request := httptest.NewRequest(http.MethodGet, "/pricing-schedules", nil)
	request.Header.Set("x-user-id", userID.String())
	request.Header.Set("x-user-name", "billing_admin")
	request.Header.Set("x-zone-id", "019f3d3e-997d-7894-9236-c5122634cb4f")
	request.Header.Set("x-tenant-id", "platform")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("expected personal platform-scope context to use personal platform-range authorization, got %d: %s", response.Code, response.Body.String())
	}
}
