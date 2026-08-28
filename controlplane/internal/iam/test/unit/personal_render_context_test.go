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

func TestPersonalRenderContextServiceUsesPlatformRoleAndDeduplicates(t *testing.T) {
	registry := cacheengine.NewCacheRegistry(cacheengine.NewL1Cache(), observability.NewNoopCacheRecorder())
	userID := uuid.New()
	cacheengine.Register(registry, "user_role", time.Minute, func(_ context.Context, param string) (*iamproto.RoleEntry, error) {
		if param != userID.String() {
			t.Fatalf("unexpected personal loader key: %s", param)
		}
		return &iamproto.RoleEntry{Permissions: []string{
			"alice:00000000-0000-0000-0000-000000000000:storage:bucket:write",
			"alice:00000000-0000-0000-0000-000000000000:storage:bucket:read",
			"alice:00000000-0000-0000-0000-000000000000:storage:bucket:read",
		}}, nil
	})

	service := iamService.NewPersonalRenderContextService(registry, observability.NewNoopWorkflowRecorder())
	personal := &iamEntity.PersonalRenderContext{UserID: userID}
	if err := service.GetPersonalRenderContext(context.Background(), personal); err != nil {
		t.Fatalf("personal render context: %v", err)
	}

	if len(personal.Capabilities) != 2 || len(personal.NavigationKeys) != 2 {
		t.Fatalf("personal projection was not deterministically deduplicated: %#v", personal)
	}
}

func TestPersonalRenderContextServicePropagatesForbiddenWhenEmpty(t *testing.T) {
	registry := cacheengine.NewCacheRegistry(cacheengine.NewL1Cache(), observability.NewNoopCacheRecorder())
	userID := uuid.New()
	cacheengine.Register(registry, "user_role", time.Minute, func(_ context.Context, _ string) (*iamproto.RoleEntry, error) {
		return &iamproto.RoleEntry{Permissions: []string{}}, nil
	})

	service := iamService.NewPersonalRenderContextService(registry, observability.NewNoopWorkflowRecorder())
	err := service.GetPersonalRenderContext(context.Background(), &iamEntity.PersonalRenderContext{UserID: userID})
	if !errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
		t.Fatalf("expected forbidden taxonomy on empty permissions, got %v", err)
	}
}

type personalRenderContextServiceStub struct {
	calls int
	err   error
}

func (s *personalRenderContextServiceStub) GetPersonalRenderContext(_ context.Context, workflow *iamEntity.PersonalRenderContext) error {
	s.calls++
	workflow.NavigationKeys = []string{"storage:bucket"}
	workflow.NavigationActions = []string{"read"}
	workflow.Capabilities = []string{"alice:00000000-0000-0000-0000-000000000000:storage:bucket:read"}
	return s.err
}

func TestPersonalRenderContextHandlerReturnsPersonalContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	service := &personalRenderContextServiceStub{}
	handler := iamHandler.NewPersonalRenderContextHandler(service)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/personal/iam/context/read", nil)
	c.Set(pkgcontext.CtxUserID, userID)
	handler.GetPersonalRenderContext(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("personal status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode personal response: %v", err)
	}
	if response.Data["kind"] != "personal" {
		t.Fatalf("personal discriminator missing: %s", recorder.Body.String())
	}
	if _, ok := response.Data["navigation"].([]any); !ok {
		t.Fatalf("personal navigation contract mismatch: %s", recorder.Body.String())
	}
	if _, ok := response.Data["capabilities"].(map[string]any); !ok {
		t.Fatalf("personal capabilities contract mismatch: %s", recorder.Body.String())
	}
	if _, exists := response.Data["navigation_keys"]; exists {
		t.Fatalf("personal response exposed internal projection arrays: %s", recorder.Body.String())
	}
	if _, exists := response.Data["tenant_id"]; exists {
		t.Fatal("personal response leaked tenant_id")
	}
}

func TestPersonalRenderHandlerRejectsConcreteTenantContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &personalRenderContextServiceStub{err: errors.New("must not be called")}
	handler := iamHandler.NewPersonalRenderContextHandler(service)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/personal/iam/context/read", nil)
	c.Set(pkgcontext.CtxUserID, uuid.New())
	c.Set(pkgcontext.CtxTenantID, uuid.New())
	handler.GetPersonalRenderContext(c)

	if recorder.Code != http.StatusForbidden || service.calls != 0 {
		t.Fatalf("personal tenant fence failed: status=%d calls=%d", recorder.Code, service.calls)
	}
}

func TestPersonalRenderHandlerRejectsMissingUserContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &personalRenderContextServiceStub{err: errors.New("must not be called")}
	handler := iamHandler.NewPersonalRenderContextHandler(service)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/personal/iam/context/read", nil)
	handler.GetPersonalRenderContext(c)

	if recorder.Code != http.StatusUnauthorized || service.calls != 0 {
		t.Fatalf("personal missing user id fence failed: status=%d calls=%d", recorder.Code, service.calls)
	}
}
