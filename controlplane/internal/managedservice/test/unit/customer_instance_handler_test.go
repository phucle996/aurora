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
		NetworkContract: entity.PersonalNetworkContract{Namespace: "aur-ms-p-scope", Components: []entity.PersonalNetworkComponent{{ComponentCode: "primary", ServiceName: "orders-db", PodSelector: map[string]string{"aurora.io/instance": "orders-db", "aurora.io/component": "primary"}, Ports: []entity.PersonalNetworkPort{{Name: "postgres", Port: 5432, Protocol: "TCP"}}}}},
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
	for _, networkField := range []string{"aur-ms-p-scope", "orders-db", "aurora.io/instance", "postgres"} {
		if !strings.Contains(response.Body.String(), networkField) {
			t.Fatalf("instance detail omitted network contract field %q", networkField)
		}
	}
}

func TestPersonalCreateCanonicalizesAtHandlerAndBuildsDistinctDesiredHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &mocks.PersonalInstanceService{CreateResult: &entity.CreatePersonalInstanceResult{
		ID: customerInstanceID, Code: "orders-db", Name: "Orders database", DesiredState: "provisioning", Generation: 1,
		OperationID: uuid.MustParse("10000000-0000-7000-8000-000000000042"), OperationKind: "create", OperationState: "accepted",
	}}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(pkgcontext.CtxUserID, customerUserID)
		c.Set(pkgcontext.CtxWorkspaceID, customerWorkspaceID)
		c.Set(pkgcontext.CtxZoneID, customerZoneID)
		c.Next()
	})
	router.POST("/instances", handler.NewPersonalInstanceHandler(service).CreatePersonalInstance)

	response := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"code":"ORDERS-DB","name":"  Orders database  ","blueprint_revision_id":"10000000-0000-7000-8000-000000000043","input_schema_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","parameters":{"storage":"100Gi","replicas":3}}`)
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/instances", body))
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", response.Code, response.Body.String())
	}
	if service.CreateCalls != 1 || service.CreateInput.Code != "orders-db" || service.CreateInput.Name != "Orders database" {
		t.Fatal("create handler did not emit one normalized workflow entity")
	}
	if service.CreateInput.UserID != customerUserID || service.CreateInput.WorkspaceID != customerWorkspaceID || service.CreateInput.ZoneID != customerZoneID {
		t.Fatal("create handler did not use trusted personal scope")
	}
	if len(service.CreateInput.InputSHA256) != 32 || len(service.CreateInput.DesiredSpecSHA256) != 32 || len(service.CreateInput.CreateIntentSHA256) != 32 {
		t.Fatal("create handler did not freeze all canonical hashes")
	}
	if bytes.Equal(service.CreateInput.InputSHA256, service.CreateInput.DesiredSpecSHA256) {
		t.Fatal("desired spec hash must bind routing/revision context, not alias the input hash")
	}

	rejected := httptest.NewRecorder()
	badBody := bytes.NewBufferString(`{"code":"orders-db","name":"Orders database","blueprint_revision_id":"10000000-0000-7000-8000-000000000043","input_schema_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","parameters":{"nested":{"replicas":3}}}`)
	router.ServeHTTP(rejected, httptest.NewRequest(http.MethodPost, "/instances", badBody))
	if rejected.Code != http.StatusBadRequest || service.CreateCalls != 1 {
		t.Fatal("nested transport input must stop before the create service")
	}
}

func TestTenantResizeUsesTrustedScopeAndRejectsArbitraryRuntimeMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &mocks.TenantInstanceService{ResizeResult: &entity.ResizeTenantInstanceResult{
		ID: customerInstanceID, Code: "shared-kafka", Generation: 4,
		OperationID: uuid.MustParse("10000000-0000-7000-8000-000000000044"), OperationKind: "resize", OperationState: "accepted",
	}}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(pkgcontext.CtxUserID, customerUserID)
		c.Set(pkgcontext.CtxTenantID, customerTenantID)
		c.Set(pkgcontext.CtxWorkspaceID, customerWorkspaceID)
		c.Set(pkgcontext.CtxZoneID, customerZoneID)
		c.Next()
	})
	router.POST("/instances/:code/resize", handler.NewTenantInstanceHandler(service).ResizeTenantInstance)

	response := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"expected_generation":3,"resources":{"replicas":5,"storage":"500Gi"}}`)
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/instances/SHARED-KAFKA/resize", body))
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", response.Code, response.Body.String())
	}
	if service.ResizeCalls != 1 || service.ResizeInput.ActorUserID != customerUserID || service.ResizeInput.TenantID != customerTenantID || service.ResizeInput.WorkspaceID != customerWorkspaceID || service.ResizeInput.ZoneID != customerZoneID || service.ResizeInput.Code != "shared-kafka" {
		t.Fatal("resize handler did not forward one trusted tenant entity")
	}

	rejected := httptest.NewRecorder()
	metadataBody := bytes.NewBufferString(`{"expected_generation":3,"resources":{"labels":{"public":"true"}}}`)
	router.ServeHTTP(rejected, httptest.NewRequest(http.MethodPost, "/instances/shared-kafka/resize", metadataBody))
	if rejected.Code != http.StatusBadRequest || service.ResizeCalls != 1 {
		t.Fatal("arbitrary runtime metadata must stop at the transport boundary")
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
