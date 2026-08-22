package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestTenantAuthorizationMiddlewareRequiresBothRedisBoundaries(t *testing.T) {
	if _, err := NewTenantAuthorizationMiddleware(nil, nil); err == nil {
		t.Fatal("expected middleware construction to reject missing Redis boundaries")
	}
}

func TestTenantAuthorizationMiddlewareRequiresExactTenantPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID, tenantID := uuid.New(), uuid.New()
	required := tenantID.String() + ":00000000-0000-0000-0000-000000000000:billing:wallet:top_up"
	authorization := &TenantAuthorizationMiddleware{l1: map[string]tenantAuthorizationCacheEntry{
		tenantID.String() + ":" + userID.String(): {permissions: map[string]struct{}{required: {}}, expiresAt: time.Now().Add(time.Minute)},
	}}
	router := gin.New()
	router.Use(ContextInjector())
	router.POST("/wallet/top-ups", authorization.Authorize("billing:wallet:top_up", false), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	request := httptest.NewRequest(http.MethodPost, "/wallet/top-ups", nil)
	request.Header.Set("x-user-id", userID.String())
	request.Header.Set("x-tenant-id", tenantID.String())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("expected exact tenant permission to pass, got %d: %s", response.Code, response.Body.String())
	}
}

func TestTenantAuthorizationMiddlewareRejectsCrossTenantPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID, tenantID, otherTenantID := uuid.New(), uuid.New(), uuid.New()
	authorization := &TenantAuthorizationMiddleware{l1: map[string]tenantAuthorizationCacheEntry{
		tenantID.String() + ":" + userID.String(): {permissions: map[string]struct{}{
			otherTenantID.String() + ":00000000-0000-0000-0000-000000000000:billing:wallet:top_up": {},
			"billing:wallet:top_up": {},
		}, expiresAt: time.Now().Add(time.Minute)},
	}}
	router := gin.New()
	router.Use(ContextInjector())
	router.POST("/wallet/top-ups", authorization.Authorize("billing:wallet:top_up", false), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	request := httptest.NewRequest(http.MethodPost, "/wallet/top-ups", nil)
	request.Header.Set("x-user-id", userID.String())
	request.Header.Set("x-tenant-id", tenantID.String())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected cross-tenant and flattened permissions to fail, got %d", response.Code)
	}
}
