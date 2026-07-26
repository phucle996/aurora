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

func (f fakeAuthorizationResolver) ResolveTenant(context.Context, uuid.UUID, uuid.UUID, bool) (map[string]struct{}, error) {
	return f.permissions, f.err
}

type tenantAuthorizationCall struct {
	userID   uuid.UUID
	tenantID uuid.UUID
	critical bool
}

type trackingTenantAuthorizationResolver struct {
	permissions map[string]struct{}
	calls       []tenantAuthorizationCall
}

func (r *trackingTenantAuthorizationResolver) Resolve(context.Context, uuid.UUID, bool) (map[string]struct{}, error) {
	return nil, nil
}

func (r *trackingTenantAuthorizationResolver) ResolveTenant(
	_ context.Context,
	userID uuid.UUID,
	tenantID uuid.UUID,
	critical bool,
) (map[string]struct{}, error) {
	r.calls = append(r.calls, tenantAuthorizationCall{
		userID:   userID,
		tenantID: tenantID,
		critical: critical,
	})
	return r.permissions, nil
}

func TestRequireIdentityAndAuthorize(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ContextInjector())
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

func TestAuthorizeTenantRequiresExactFivePartPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	tenantID := uuid.New()
	required := tenantID.String() +
		":00000000-0000-0000-0000-000000000000:billing:wallet:top_up"
	resolver := &trackingTenantAuthorizationResolver{
		permissions: map[string]struct{}{required: {}},
	}
	router := gin.New()
	router.Use(ContextInjector())
	router.POST(
		"/wallet/top-ups",
		AuthorizeTenant(resolver, "billing:wallet:top_up", true),
		func(c *gin.Context) { c.Status(http.StatusNoContent) },
	)

	request := httptest.NewRequest(http.MethodPost, "/wallet/top-ups", nil)
	request.Header.Set("x-user-id", userID.String())
	request.Header.Set("x-tenant-id", tenantID.String())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("expected exact tenant permission to pass, got %d: %s", response.Code, response.Body.String())
	}
	if len(resolver.calls) != 1 {
		t.Fatalf("expected one tenant authorization lookup, got %d", len(resolver.calls))
	}
	call := resolver.calls[0]
	if call.userID != userID || call.tenantID != tenantID || !call.critical {
		t.Fatalf("unexpected authorization lookup: %#v", call)
	}
}

func TestAuthorizeTenantRejectsCrossTenantAndFlattenedPermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	requestTenantID := uuid.New()
	otherTenantID := uuid.New()
	resolver := &trackingTenantAuthorizationResolver{
		permissions: map[string]struct{}{
			otherTenantID.String() + ":00000000-0000-0000-0000-000000000000:billing:wallet:top_up": {},
			"billing:wallet:top_up": {},
		},
	}
	router := gin.New()
	router.Use(ContextInjector())
	router.POST(
		"/wallet/top-ups",
		AuthorizeTenant(resolver, "billing:wallet:top_up", true),
		func(c *gin.Context) { c.Status(http.StatusNoContent) },
	)

	request := httptest.NewRequest(http.MethodPost, "/wallet/top-ups", nil)
	request.Header.Set("x-user-id", userID.String())
	request.Header.Set("x-tenant-id", requestTenantID.String())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("expected cross-tenant and flattened permissions to fail, got %d", response.Code)
	}
}

func TestAuthorizeTenantRejectsPlatformSentinel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resolver := &trackingTenantAuthorizationResolver{}
	router := gin.New()
	router.Use(ContextInjector())
	router.GET(
		"/wallet",
		AuthorizeTenant(resolver, "billing:wallet:read", false),
		func(c *gin.Context) { c.Status(http.StatusNoContent) },
	)

	request := httptest.NewRequest(http.MethodGet, "/wallet", nil)
	request.Header.Set("x-user-id", uuid.NewString())
	request.Header.Set("x-tenant-id", "platform")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("expected platform sentinel to fail tenant authorization, got %d", response.Code)
	}
	if len(resolver.calls) != 0 {
		t.Fatal("invalid tenant context reached the authorization resolver")
	}
}
