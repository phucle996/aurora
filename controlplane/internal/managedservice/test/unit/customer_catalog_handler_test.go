package unit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"controlplane/internal/managedservice/domain/entity"
	"controlplane/internal/managedservice/taxonomy"
	"controlplane/internal/managedservice/test/mocks"
	"controlplane/internal/managedservice/transport/http/handler"
	"controlplane/pkg/apires"
	pkgcontext "controlplane/pkg/context"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var (
	customerUserID      = uuid.MustParse("10000000-0000-4000-8000-000000000001")
	customerTenantID    = uuid.MustParse("10000000-0000-4000-8000-000000000002")
	customerWorkspaceID = uuid.MustParse("10000000-0000-4000-8000-000000000011")
	customerZoneID      = uuid.MustParse("10000000-0000-4000-8000-000000000021")
	customerVersionID   = uuid.MustParse("10000000-0000-4000-8000-000000000033")
	customerRevisionID  = uuid.MustParse("10000000-0000-4000-8000-000000000035")
)

func TestPersonalCatalogHandlerUsesTrustedContextAndExcludesProtectedArtifacts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &mocks.PersonalCatalogService{Result: &entity.PersonalCatalogPage{Items: []entity.PersonalCatalogItem{{
		CategoryID: customerVersionID, CategoryCode: "messaging", CategoryNameI18n: json.RawMessage(`{"en":"Messaging"}`), CategoryDescriptionI18n: json.RawMessage(`{}`),
		DefinitionID: customerVersionID, DefinitionCode: "apache-kafka", DefinitionNameI18n: json.RawMessage(`{"en":"Apache Kafka"}`), DefinitionDescriptionI18n: json.RawMessage(`{}`),
		VersionID: customerVersionID, VersionCode: "3.8", VersionDisplay: "3.8", VersionNameI18n: json.RawMessage(`{"en":"Kafka 3.8"}`), VersionDescriptionI18n: json.RawMessage(`{}`),
		RevisionID: customerRevisionID, RevisionNumber: 3, ContractVersion: "platform-form/v1", ContractSHA256: make([]byte, 32),
	}}}}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(pkgcontext.CtxUserID, customerUserID)
		c.Set(pkgcontext.CtxWorkspaceID, customerWorkspaceID)
		c.Set(pkgcontext.CtxZoneID, customerZoneID)
		c.Next()
	})
	router.GET("/catalog", handler.NewPersonalCatalogHandler(service).ListPersonalCatalog)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/catalog?limit=25", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if service.Calls != 1 || service.Input.UserID != customerUserID || service.Input.WorkspaceID != customerWorkspaceID || service.Input.ZoneID != customerZoneID || service.Input.Limit != 25 {
		t.Fatal("handler did not forward the normalized trusted personal scope")
	}
	body := response.Body.String()
	var envelope apires.APIResponse
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode apires envelope: %v", err)
	}
	if envelope.Message != "managed service catalog fetched" || envelope.Data == nil {
		t.Fatalf("success must use the shared apires envelope: %#v", envelope)
	}
	for _, protected := range []string{"template_yaml", "component_contract", "zone_selector", "capability_requirement"} {
		if strings.Contains(body, protected) {
			t.Fatalf("customer response leaked protected field %q", protected)
		}
	}
	if response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("scoped catalog response must not enter a shared HTTP cache")
	}
}

func TestPersonalCatalogHandlerRejectsTenantContextBeforeService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &mocks.PersonalCatalogService{Result: &entity.PersonalCatalogPage{}}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(pkgcontext.CtxTenantID, customerTenantID)
		c.Next()
	})
	router.GET("/catalog", handler.NewPersonalCatalogHandler(service).ListPersonalCatalog)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/catalog", nil))
	if response.Code != http.StatusForbidden || service.Calls != 0 {
		t.Fatalf("tenant context must fail before personal service, got %d", response.Code)
	}
}

func TestPersonalVersionMapsStaleRevisionWithoutReturningAForm(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &mocks.PersonalCatalogVersionService{Err: taxonomy.ErrCustomerCatalogStale}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(pkgcontext.CtxUserID, customerUserID)
		c.Set(pkgcontext.CtxWorkspaceID, customerWorkspaceID)
		c.Set(pkgcontext.CtxZoneID, customerZoneID)
		c.Next()
	})
	router.GET("/catalog/versions/:version_id", handler.NewPersonalCatalogVersionHandler(service).GetPersonalCatalogVersion)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/catalog/versions/"+customerVersionID.String()+"?expected_revision_id="+customerRevisionID.String(), nil)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "conflict") {
		t.Fatalf("expected stable stale taxonomy, got %d: %s", response.Code, response.Body.String())
	}
	var envelope apires.APIResponse
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || envelope.Error != "conflict" {
		t.Fatalf("stale response must use the shared apires taxonomy envelope: %#v, err=%v", envelope, err)
	}
	if service.Calls != 1 || service.Input.ExpectedRevisionID != customerRevisionID {
		t.Fatal("handler did not forward the validated revision fence")
	}
}

func TestTenantCatalogHandlerForwardsTenantWorkspaceZoneScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &mocks.TenantCatalogService{Result: &entity.TenantCatalogPage{}}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(pkgcontext.CtxUserID, customerUserID)
		c.Set(pkgcontext.CtxTenantID, customerTenantID)
		c.Set(pkgcontext.CtxWorkspaceID, customerWorkspaceID)
		c.Set(pkgcontext.CtxZoneID, customerZoneID)
		c.Next()
	})
	router.GET("/catalog", handler.NewTenantCatalogHandler(service).ListTenantCatalog)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/catalog", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if service.Calls != 1 || service.Input.TenantID != customerTenantID || service.Input.WorkspaceID != customerWorkspaceID || service.Input.ZoneID != customerZoneID {
		t.Fatal("handler did not forward the normalized trusted tenant scope")
	}
}
