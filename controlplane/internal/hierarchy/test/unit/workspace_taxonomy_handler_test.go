package unit

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	hierarchySvcInterface "controlplane/internal/hierarchy/domain/service"
	hierarchyTaxonomy "controlplane/internal/hierarchy/taxonomy"
	hierarchyHandler "controlplane/internal/hierarchy/transport/http/handler"
	pkgcontext "controlplane/pkg/context"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type personalWorkspacePreconditionService struct {
	hierarchySvcInterface.PersonalWorkspaceService
}

func (personalWorkspacePreconditionService) CreateWorkspaceForPersonal(
	context.Context,
	*hierarchyEntity.CreatePersonalWorkspace,
) (*hierarchyEntity.CreatePersonalWorkspace, error) {
	return nil, hierarchyTaxonomy.ErrPreconditionFailed
}

type tenantWorkspacePreconditionService struct {
	hierarchySvcInterface.TenantWorkspaceService
}

func (tenantWorkspacePreconditionService) CreateWorkspaceForTenant(
	context.Context,
	*hierarchyEntity.CreateTenantWorkspace,
) (*hierarchyEntity.CreateTenantWorkspace, error) {
	return nil, hierarchyTaxonomy.ErrPreconditionFailed
}

func TestCreatePersonalWorkspaceMapsParentPreconditionToConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := hierarchyHandler.NewWorkspacePersonalHandler(personalWorkspacePreconditionService{})
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(pkgcontext.CtxZoneID, uuid.MustParse("10000000-0000-4000-8000-000000000001"))
		c.Set(pkgcontext.CtxUserID, uuid.MustParse("10000000-0000-4000-8000-000000000002"))
		c.Next()
	})
	router.POST("/api/v1/me/hierarchy/workspace/create", handler.CreateWorkspacePersonal)

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/me/hierarchy/workspace/create",
		bytes.NewBufferString(`{"name":"Personal","code":"personal"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("expected inactive parent to map to 409, got %d: %s", response.Code, response.Body.String())
	}
}

func TestCreateTenantWorkspaceMapsParentPreconditionToConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := hierarchyHandler.NewWorkspaceTenantHandler(tenantWorkspacePreconditionService{})
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(pkgcontext.CtxZoneID, uuid.MustParse("20000000-0000-4000-8000-000000000001"))
		c.Set(pkgcontext.CtxTenantID, uuid.MustParse("20000000-0000-4000-8000-000000000002"))
		c.Set(pkgcontext.CtxUserID, uuid.MustParse("20000000-0000-4000-8000-000000000003"))
		c.Next()
	})
	router.POST("/api/v1/tenant/hierarchy/workspaces", handler.CreateWorkspaceTenant)

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/tenant/hierarchy/workspaces",
		bytes.NewBufferString(`{"name":"Tenant","code":"tenant"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("expected inactive parent to map to 409, got %d: %s", response.Code, response.Body.String())
	}
}
