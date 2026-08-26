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
	iamRepository "controlplane/internal/iam/repository"
	iamService "controlplane/internal/iam/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamHandler "controlplane/internal/iam/transport/http/handler"
	iamproto "controlplane/internal/iam/transport/proto"
	"controlplane/internal/observability"
	pkgcontext "controlplane/pkg/context"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type renderContextRepositoryStub struct {
	personalCalls int
	tenantCalls   int
	err           error
}

func (s *renderContextRepositoryStub) GetPersonalRenderContext(_ context.Context, workflow *iamEntity.PersonalRenderContext) error {
	s.personalCalls++
	workflow.Permissions = []string{
		"alice:00000000-0000-0000-0000-000000000000:storage:bucket:write",
		"alice:00000000-0000-0000-0000-000000000000:storage:bucket:read",
		"alice:00000000-0000-0000-0000-000000000000:storage:bucket:read",
	}
	return s.err
}

func (s *renderContextRepositoryStub) GetTenantRenderContext(_ context.Context, workflow *iamEntity.TenantRenderContext) error {
	s.tenantCalls++
	workflow.Permissions = []string{
		workflow.TenantID.String() + ":00000000-0000-0000-0000-000000000000:managed-service:instance:read",
	}
	return s.err
}

func TestRenderContextServicesKeepOwnerWorkflowsIsolated(t *testing.T) {
	repository := &renderContextRepositoryStub{}
	service := iamService.NewRenderContextService(repository, observability.NewNoopWorkflowRecorder())

	personal := &iamEntity.PersonalRenderContext{UserID: uuid.New()}
	if err := service.GetPersonalRenderContext(context.Background(), personal); err != nil {
		t.Fatalf("personal render context: %v", err)
	}
	if repository.personalCalls != 1 || repository.tenantCalls != 0 {
		t.Fatalf("personal workflow crossed repository branch: personal=%d tenant=%d", repository.personalCalls, repository.tenantCalls)
	}
	if len(personal.Capabilities) != 2 || len(personal.NavigationKeys) != 2 {
		t.Fatalf("personal projection was not deterministically deduplicated: %#v", personal)
	}

	tenant := &iamEntity.TenantRenderContext{UserID: uuid.New(), TenantID: uuid.New()}
	if err := service.GetTenantRenderContext(context.Background(), tenant); err != nil {
		t.Fatalf("tenant render context: %v", err)
	}
	if repository.personalCalls != 1 || repository.tenantCalls != 1 {
		t.Fatalf("tenant workflow crossed repository branch: personal=%d tenant=%d", repository.personalCalls, repository.tenantCalls)
	}
}

func TestRenderContextRepositoryUsesSeparateL1NamespacesAndFailsClosed(t *testing.T) {
	registry := cacheengine.NewCacheRegistry(cacheengine.NewL1Cache(), observability.NewNoopCacheRecorder())
	userID := uuid.New()
	tenantID := uuid.New()
	cacheengine.Register(registry, "user_role", time.Minute, func(_ context.Context, param string) (*iamproto.RoleEntry, error) {
		if param != userID.String() {
			t.Fatalf("unexpected personal loader key: %s", param)
		}
		return &iamproto.RoleEntry{Permissions: []string{"alice:00000000-0000-0000-0000-000000000000:iam:profile:read"}}, nil
	})
	tenantLoaderCalls := 0
	cacheengine.Register(registry, "membership_role", time.Minute, func(_ context.Context, param string) (*iamproto.RoleEntry, error) {
		tenantLoaderCalls++
		if tenantLoaderCalls == 1 && param != userID.String()+":"+tenantID.String() {
			t.Fatalf("unexpected tenant loader key: %s", param)
		}
		return &iamproto.RoleEntry{Permissions: []string{tenantID.String() + ":00000000-0000-0000-0000-000000000000:iam:member:read"}}, nil
	})

	repository := iamRepository.NewRenderContextRepository(registry)
	personal := &iamEntity.PersonalRenderContext{UserID: userID}
	if err := repository.GetPersonalRenderContext(context.Background(), personal); err != nil {
		t.Fatalf("load personal L1: %v", err)
	}
	tenant := &iamEntity.TenantRenderContext{UserID: userID, TenantID: tenantID}
	if err := repository.GetTenantRenderContext(context.Background(), tenant); err != nil {
		t.Fatalf("load tenant L1: %v", err)
	}

	otherTenant := &iamEntity.TenantRenderContext{UserID: userID, TenantID: uuid.New()}
	if err := repository.GetTenantRenderContext(context.Background(), otherTenant); err == nil {
		t.Fatal("tenant identity mismatch must fail closed")
	}
}

type renderContextServiceStub struct {
	personalCalls int
	tenantCalls   int
	err           error
}

func (s *renderContextServiceStub) GetPersonalRenderContext(_ context.Context, workflow *iamEntity.PersonalRenderContext) error {
	s.personalCalls++
	workflow.NavigationKeys = []string{"storage:bucket"}
	workflow.NavigationActions = []string{"read"}
	workflow.Capabilities = []string{"alice:00000000-0000-0000-0000-000000000000:storage:bucket:read"}
	return s.err
}

func (s *renderContextServiceStub) GetTenantRenderContext(_ context.Context, workflow *iamEntity.TenantRenderContext) error {
	s.tenantCalls++
	workflow.NavigationKeys = []string{"storage:bucket"}
	workflow.NavigationActions = []string{"read"}
	workflow.Capabilities = []string{workflow.TenantID.String() + ":00000000-0000-0000-0000-000000000000:storage:bucket:read"}
	return s.err
}

func TestRenderContextHandlersReturnDiscriminatedContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userID := uuid.New()
	tenantID := uuid.New()
	service := &renderContextServiceStub{}
	handler := iamHandler.NewRenderContextHandler(service)

	personalRecorder := httptest.NewRecorder()
	personalContext, _ := gin.CreateTestContext(personalRecorder)
	personalContext.Request = httptest.NewRequest(http.MethodGet, "/api/v1/personal/iam/context/read", nil)
	personalContext.Set(pkgcontext.CtxUserID, userID)
	handler.GetPersonalRenderContext(personalContext)
	if personalRecorder.Code != http.StatusOK {
		t.Fatalf("personal status=%d body=%s", personalRecorder.Code, personalRecorder.Body.String())
	}
	var personalResponse struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(personalRecorder.Body.Bytes(), &personalResponse); err != nil {
		t.Fatalf("decode personal response: %v", err)
	}
	if personalResponse.Data["kind"] != "personal" {
		t.Fatalf("personal discriminator missing: %s", personalRecorder.Body.String())
	}
	if _, exists := personalResponse.Data["tenant_id"]; exists {
		t.Fatal("personal response leaked tenant_id")
	}

	tenantRecorder := httptest.NewRecorder()
	tenantContext, _ := gin.CreateTestContext(tenantRecorder)
	tenantContext.Request = httptest.NewRequest(http.MethodGet, "/api/v1/tenant/iam/context/read", nil)
	tenantContext.Set(pkgcontext.CtxUserID, userID)
	tenantContext.Set(pkgcontext.CtxTenantID, tenantID)
	handler.GetTenantRenderContext(tenantContext)
	if tenantRecorder.Code != http.StatusOK {
		t.Fatalf("tenant status=%d body=%s", tenantRecorder.Code, tenantRecorder.Body.String())
	}
	var tenantResponse struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(tenantRecorder.Body.Bytes(), &tenantResponse); err != nil {
		t.Fatalf("decode tenant response: %v", err)
	}
	if tenantResponse.Data["kind"] != "tenant" || tenantResponse.Data["tenant_id"] != tenantID.String() {
		t.Fatalf("tenant discriminator mismatch: %s", tenantRecorder.Body.String())
	}
}

func TestPersonalRenderHandlerRejectsTenantContextBeforeService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &renderContextServiceStub{err: errors.New("must not be called")}
	handler := iamHandler.NewRenderContextHandler(service)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/personal/iam/context/read", nil)
	c.Set(pkgcontext.CtxUserID, uuid.New())
	c.Set(pkgcontext.CtxTenantID, uuid.New())
	handler.GetPersonalRenderContext(c)
	if recorder.Code != http.StatusForbidden || service.personalCalls != 0 {
		t.Fatalf("personal tenant fence failed: status=%d calls=%d", recorder.Code, service.personalCalls)
	}
}

func TestRenderContextServicePropagatesForbiddenTaxonomy(t *testing.T) {
	repository := &renderContextRepositoryStub{err: iamTaxonomy.ErrActionNotAllowed}
	service := iamService.NewRenderContextService(repository, observability.NewNoopWorkflowRecorder())
	err := service.GetPersonalRenderContext(context.Background(), &iamEntity.PersonalRenderContext{UserID: uuid.New()})
	if !errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
		t.Fatalf("expected forbidden taxonomy, got %v", err)
	}
}
