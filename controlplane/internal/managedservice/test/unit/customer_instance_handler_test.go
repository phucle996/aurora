package unit

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"controlplane/internal/managedservice/domain/entity"
	"controlplane/internal/managedservice/taxonomy"
	"controlplane/internal/managedservice/test/mocks"
	"controlplane/internal/managedservice/transport/http/handler"
	pkgcontext "controlplane/pkg/context"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var customerInstanceID = uuid.MustParse("10000000-0000-7000-8000-000000000041")

func TestPersonalInstanceDetailUsesTrustedScopeAndExcludesProtectedInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &mocks.PersonalInstanceService{GetResult: &entity.PersonalInstanceDetail{
		ID: customerInstanceID, Code: "orders-db", Name: "Orders database", DesiredState: "active",
		Generation: 3, RevisionSequence: 2, ObservedState: "ready", ObservedStateVersion: 7,
		ObservedOutput: json.RawMessage(`{"endpoint":"db.internal"}`), MetadataVersion: 4,
		CreatedAt: time.Unix(10, 0).UTC(), UpdatedAt: time.Unix(20, 0).UTC(),
	}}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(pkgcontext.CtxUserID, customerUserID)
		c.Set(pkgcontext.CtxWorkspaceID, customerWorkspaceID)
		c.Set(pkgcontext.CtxZoneID, customerZoneID)
		c.Next()
	})
	router.GET("/instances/:code", handler.NewPersonalInstanceHandler(service).GetPersonalInstance)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/instances/orders-db", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if service.GetCalls != 1 || service.GetInput.UserID != customerUserID || service.GetInput.WorkspaceID != customerWorkspaceID || service.GetInput.ZoneID != customerZoneID || service.GetInput.Code != "orders-db" {
		t.Fatal("handler did not forward normalized trusted personal scope")
	}
	for _, protected := range []string{"parameter_envelope", "input_sha256", "desired_spec_sha256", "create_intent_sha256"} {
		if strings.Contains(response.Body.String(), protected) {
			t.Fatalf("instance detail leaked protected field %q", protected)
		}
	}
	if response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("owner-scoped instance response must not enter a shared HTTP cache")
	}
}

func TestPersonalRenameValidatesAndNormalizesOnlyAtHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &mocks.PersonalInstanceService{RenameResult: &entity.RenamePersonalInstanceResult{
		ID: customerInstanceID, Code: "orders-db", Name: "Orders primary", MetadataVersion: 5, UpdatedAt: time.Unix(30, 0).UTC(),
	}}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(pkgcontext.CtxUserID, customerUserID)
		c.Set(pkgcontext.CtxWorkspaceID, customerWorkspaceID)
		c.Set(pkgcontext.CtxZoneID, customerZoneID)
		c.Next()
	})
	router.PATCH("/instances/:code/name", handler.NewPersonalInstanceHandler(service).RenamePersonalInstance)

	response := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"name":"  Orders primary  ","expected_metadata_version":4}`)
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/instances/ORDERS-DB/name", body))
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if service.RenameCalls != 1 || service.RenameInput.Name != "Orders primary" || service.RenameInput.Code != "orders-db" || service.RenameInput.ExpectedMetadataVersion != 4 {
		t.Fatal("handler did not pass one normalized rename entity")
	}

	rejected := httptest.NewRecorder()
	badBody := bytes.NewBufferString(`{"name":"ok","expected_metadata_version":0,"extra":true}`)
	router.ServeHTTP(rejected, httptest.NewRequest(http.MethodPatch, "/instances/orders-db/name", badBody))
	if rejected.Code != http.StatusBadRequest || service.RenameCalls != 1 {
		t.Fatal("invalid transport data must stop before the service")
	}
}

func TestTenantInstanceDetailForwardsPhysicalTenantScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &mocks.TenantInstanceService{GetResult: &entity.TenantInstanceDetail{
		ID: customerInstanceID, Code: "shared-kafka", Name: "Shared Kafka", DesiredState: "provisioning",
		ObservedState: "unknown", ObservedOutput: json.RawMessage(`{}`), CreatedAt: time.Unix(10, 0).UTC(), UpdatedAt: time.Unix(20, 0).UTC(),
	}}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(pkgcontext.CtxUserID, customerUserID)
		c.Set(pkgcontext.CtxTenantID, customerTenantID)
		c.Set(pkgcontext.CtxWorkspaceID, customerWorkspaceID)
		c.Set(pkgcontext.CtxZoneID, customerZoneID)
		c.Next()
	})
	router.GET("/instances/:code", handler.NewTenantInstanceHandler(service).GetTenantInstance)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/instances/shared-kafka", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if service.GetCalls != 1 || service.GetInput.ActorUserID != customerUserID || service.GetInput.TenantID != customerTenantID || service.GetInput.WorkspaceID != customerWorkspaceID || service.GetInput.ZoneID != customerZoneID {
		t.Fatal("handler did not forward actor and physical tenant/workspace/Zone scope")
	}
}

func TestTenantRenameMapsOptimisticConflictWithoutRetrying(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &mocks.TenantInstanceService{RenameErr: taxonomy.ErrConflict}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(pkgcontext.CtxUserID, customerUserID)
		c.Set(pkgcontext.CtxTenantID, customerTenantID)
		c.Set(pkgcontext.CtxWorkspaceID, customerWorkspaceID)
		c.Set(pkgcontext.CtxZoneID, customerZoneID)
		c.Next()
	})
	router.PATCH("/instances/:code/name", handler.NewTenantInstanceHandler(service).RenameTenantInstance)

	response := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"name":"Shared Kafka","expected_metadata_version":3}`)
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/instances/shared-kafka/name", body))
	if response.Code != http.StatusConflict || service.RenameCalls != 1 {
		t.Fatalf("expected one conflict response, got %d calls=%d", response.Code, service.RenameCalls)
	}
}

func TestPersonalOperationHandlerRejectsInvalidCursorBeforeService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &mocks.PersonalInstanceService{ListOperationResult: &entity.PersonalInstanceOperationPage{}}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(pkgcontext.CtxUserID, customerUserID)
		c.Set(pkgcontext.CtxWorkspaceID, customerWorkspaceID)
		c.Set(pkgcontext.CtxZoneID, customerZoneID)
		c.Next()
	})
	router.GET("/instances/:code/operations", handler.NewPersonalInstanceHandler(service).ListPersonalInstanceOperations)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/instances/orders-db/operations?cursor=not-a-cursor", nil))
	if response.Code != http.StatusBadRequest || service.ListOperationCalls != 0 {
		t.Fatal("invalid pagination must stop at the transport boundary")
	}
}
