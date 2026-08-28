package unit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"controlplane/internal/cacheengine"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamService "controlplane/internal/iam/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamHandler "controlplane/internal/iam/transport/http/handler"
	iamproto "controlplane/internal/iam/transport/proto"
	"controlplane/internal/observability"
	pkgcontext "controlplane/pkg/context"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestTenantRenderContextServiceUsesMembershipRole(t *testing.T) {
	registry := cacheengine.NewCacheRegistry(cacheengine.NewL1Cache(), observability.NewNoopCacheRecorder())
	userID := uuid.New()
	tenantID := uuid.New()
	cacheengine.Register(registry, "membership_role", time.Minute, func(_ context.Context, param string) (*iamproto.RoleEntry, error) {
		if param != userID.String()+":"+tenantID.String() {
			t.Fatalf("unexpected tenant loader key: %s", param)
		}
		return &iamproto.RoleEntry{Permissions: []string{
			tenantID.String() + ":00000000-0000-0000-0000-000000000000:managed-service:instance:read",
		}}, nil
	})

	service := iamService.NewTenantRenderContextService(registry, observability.NewNoopWorkflowRecorder())
	tenant := &iamEntity.TenantRenderContext{UserID: userID, TenantID: tenantID}
	if err := service.GetTenantRenderContext(context.Background(), tenant); err != nil {
		t.Fatalf("tenant render context: %v", err)
	}

	if len(tenant.Capabilities) != 1 || len(tenant.NavigationKeys) != 1 {
		t.Fatalf("tenant projection unexpected: %#v", tenant)
	}
}

func TestTenantRenderContextServicePropagatesForbiddenWhenEmpty(t *testing.T) {
	registry := cacheengine.NewCacheRegistry(cacheengine.NewL1Cache(), observability.NewNoopCacheRecorder())
	userID := uuid.New()
	tenantID := uuid.New()
	cacheengine.Register(registry, "membership_role", time.Minute, func(_ context.Context, _ string) (*iamproto.RoleEntry, error) {
		return &iamproto.RoleEntry{Permissions: []string{}}, nil
	})

	service := iamService.NewTenantRenderContextService(registry, observability.NewNoopWorkflowRecorder())
	err := service.GetTenantRenderContext(context.Background(), &iamEntity.TenantRenderContext{UserID: userID, TenantID: tenantID})
	if !errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
		t.Fatalf("expected forbidden taxonomy on empty permissions, got %v", err)
	}
}

func TestTenantRenderContextServiceRejectsMismatchedTenantPrefix(t *testing.T) {
	registry := cacheengine.NewCacheRegistry(cacheengine.NewL1Cache(), observability.NewNoopCacheRecorder())
	userID := uuid.New()
	tenantID := uuid.New()
	cacheengine.Register(registry, "membership_role", time.Minute, func(_ context.Context, _ string) (*iamproto.RoleEntry, error) {
		return &iamproto.RoleEntry{Permissions: []string{
			uuid.NewString() + ":00000000-0000-0000-0000-000000000000:storage:bucket:read",
		}}, nil
	})

	service := iamService.NewTenantRenderContextService(registry, observability.NewNoopWorkflowRecorder())
	if err := service.GetTenantRenderContext(context.Background(), &iamEntity.TenantRenderContext{UserID: userID, TenantID: tenantID}); err == nil {
		t.Fatal("tenant identity mismatch must fail closed")
	}
}

type tenantRenderContextServiceStub struct {
	calls int
	err   error
}

func (s *tenantRenderContextServiceStub) GetTenantRenderContext(_ context.Context, workflow *iamEntity.TenantRenderContext) error {
	s.calls++
	workflow.NavigationKeys = []string{"storage:bucket"}
	workflow.NavigationActions = []string{"read"}
	workflow.Capabilities = []string{workflow.TenantID.String() + ":00000000-0000-0000-0000-000000000000:storage:bucket:read"}
	return s.err
}

func TestTenantRenderContextHandlerReturnsTenantContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	tenantID := uuid.New()
	service := &tenantRenderContextServiceStub{}
	handler := iamHandler.NewTenantRenderContextHandler(service)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/tenant/iam/context/read", nil)
	c.Set(pkgcontext.CtxUserID, userID)
	c.Set(pkgcontext.CtxTenantID, tenantID)
	handler.GetTenantRenderContext(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("tenant status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode tenant response: %v", err)
	}
	if response.Data["kind"] != "tenant" || response.Data["tenant_id"] != tenantID.String() {
		t.Fatalf("tenant discriminator mismatch: %s", recorder.Body.String())
	}
	if _, ok := response.Data["navigation"].([]any); !ok {
		t.Fatalf("tenant navigation contract mismatch: %s", recorder.Body.String())
	}
	if _, ok := response.Data["capabilities"].(map[string]any); !ok {
		t.Fatalf("tenant capabilities contract mismatch: %s", recorder.Body.String())
	}
	if _, exists := response.Data["navigation_keys"]; exists {
		t.Fatalf("tenant response exposed internal projection arrays: %s", recorder.Body.String())
	}
}

func TestTenantRenderHandlerRejectsMissingTenantContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &tenantRenderContextServiceStub{err: errors.New("must not be called")}
	handler := iamHandler.NewTenantRenderContextHandler(service)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/tenant/iam/context/read", nil)
	c.Set(pkgcontext.CtxUserID, uuid.New())
	handler.GetTenantRenderContext(c)

	if recorder.Code != http.StatusBadRequest || service.calls != 0 {
		t.Fatalf("tenant missing fence failed: status=%d calls=%d", recorder.Code, service.calls)
	}
}
