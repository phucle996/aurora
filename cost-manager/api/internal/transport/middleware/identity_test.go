package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type fakeAuthorizationResolver struct {
	permissions map[string]struct{}
	err         error
}

func (f fakeAuthorizationResolver) Resolve(context.Context, uuid.UUID, bool) (map[string]struct{}, error) {
	return f.permissions, f.err
}

func TestRequireIdentityAndAuthorize(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ContextInjector())
	router.Use(RequireIdentity())
	resolver := fakeAuthorizationResolver{permissions: map[string]struct{}{"billing:tier:read": {}}}
	router.GET("/tiers", Authorize(resolver, "billing:tier:read", false), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodGet, "/tiers", nil)
	request.Header.Set("x-user-id", "00000000-0000-0000-0000-000000000005")
	request.Header.Set("x-user-name", "billing_admin")
	request.Header.Set("x-zone-id", "019f3d3e-997d-7894-9236-c5122634cb4f")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("expected authorized response, got %d: %s", response.Code, response.Body.String())
	}
}

func TestAuthorizeRejectsMissingPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ContextInjector())
	router.Use(RequireIdentity())
	router.GET("/tiers", Authorize(fakeAuthorizationResolver{permissions: map[string]struct{}{"billing:plan:read": {}}}, "billing:tier:read", false), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodGet, "/tiers", nil)
	request.Header.Set("x-user-id", "00000000-0000-0000-0000-000000000005")
	request.Header.Set("x-user-name", "billing_admin")
	request.Header.Set("x-zone-id", "019f3d3e-997d-7894-9236-c5122634cb4f")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected missing permission to be rejected, got %d", response.Code)
	}
}

func TestRequireSessionProofFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/critical", RequireSessionProof(), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/critical", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected missing session proof to be forbidden, got %d", response.Code)
	}
}
