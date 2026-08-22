package unit

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	hierarchySvcInterface "controlplane/internal/hierarchy/domain/service"
	hierarchyHandler "controlplane/internal/hierarchy/transport/http/handler"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type tenantCreateCapture struct {
	hierarchySvcInterface.TenantService
	input *hierarchyEntity.CreateTenant
}

func (capture *tenantCreateCapture) CreateTenant(_ context.Context, in *hierarchyEntity.CreateTenant) (*hierarchyEntity.CreateTenant, error) {
	capture.input = in
	return &hierarchyEntity.CreateTenant{
		ID:            uuid.MustParse("10000000-0000-4000-8000-000000000010"),
		Code:          in.Code,
		Name:          in.Name,
		PrimaryDomain: in.PrimaryDomain,
		Status:        hierarchyEntity.TenantStatusActive,
		CreatedAt:     time.Unix(10, 0).UTC(),
	}, nil
}

func TestCreateTenantAcceptsPersonalPlatformSentinel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	capture := &tenantCreateCapture{}
	handler := hierarchyHandler.NewTenantHandler(capture)
	router := gin.New()
	router.POST("/tenants", handler.CreateTenant)

	request := httptest.NewRequest(http.MethodPost, "/tenants", bytes.NewBufferString(`{"name":" Acme ","code":"ACME","primary_domain":"Acme.Example"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-user-id", "10000000-0000-4000-8000-000000000001")
	request.Header.Set("x-tenant-id", "platform")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", response.Code, response.Body.String())
	}
	if capture.input == nil || capture.input.Code != "acme" || capture.input.PrimaryDomain != "acme.example" {
		t.Fatalf("handler did not canonicalize personal tenant command: %#v", capture.input)
	}
}

func TestCreateTenantRejectsConcreteTenantContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	capture := &tenantCreateCapture{}
	handler := hierarchyHandler.NewTenantHandler(capture)
	router := gin.New()
	router.POST("/tenants", handler.CreateTenant)

	request := httptest.NewRequest(http.MethodPost, "/tenants", bytes.NewBufferString(`{"name":"Acme","code":"acme","primary_domain":"acme.example"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-user-id", "10000000-0000-4000-8000-000000000001")
	request.Header.Set("x-tenant-id", "10000000-0000-4000-8000-000000000002")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", response.Code, response.Body.String())
	}
	if capture.input != nil {
		t.Fatal("concrete tenant context must not reach the service")
	}
}
