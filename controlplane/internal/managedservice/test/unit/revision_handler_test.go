package unit

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"controlplane/internal/managedservice/domain/entity"
	"controlplane/internal/managedservice/test/mocks"
	"controlplane/internal/managedservice/transport/http/handler"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestValidateDraftFailsClosedWithoutCriticalProof(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &mocks.RevisionService{}
	h := handler.NewRevisionHandler(service)
	router := gin.New()
	router.POST("/admin/critical/managed-services/catalog/drafts/:draft_id/validate", h.ValidateDraft)

	request := httptest.NewRequest(http.MethodPost, "/admin/critical/managed-services/catalog/drafts/10000000-0000-4000-8000-000000000001/validate", bytes.NewBufferString(`{}`))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("expected fail-closed 403, got %d: %s", response.Code, response.Body.String())
	}
	if service.ValidateCalls != 0 {
		t.Fatal("service must not observe a request without ACR critical proof")
	}
}

func TestValidateDraftRejectsLiteralSecretBeforeService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &mocks.RevisionService{}
	h := handler.NewRevisionHandler(service)
	router := gin.New()
	router.POST("/admin/critical/managed-services/catalog/drafts/:draft_id/validate", h.ValidateDraft)
	body := map[string]any{
		"expected_version":            1,
		"template_yaml":               "apiVersion: v1\nkind: Secret\nmetadata:\n  name: !aurora/component primary\nstringData:\n  password: plaintext\n",
		"contract_version":            "platform-form/v1",
		"component_contract":          []any{map[string]any{"id": "primary", "apply_order": 10, "delete_order": 10, "readiness": map[string]any{"type": "exists"}}},
		"input_schema":                map[string]any{"fields": []any{map[string]any{"key": "password", "value_type": "STRING", "cardinality": "ONE", "required": true, "mutable": true}}},
		"ui_schema":                   map[string]any{"groups": []any{map[string]any{"key": "general", "order": 10, "label_i18n": map[string]any{"en": "General"}}}, "fields": []any{map[string]any{"key": "password", "group": "general", "order": 10, "widget": "TEXT", "label_i18n": map[string]any{"en": "Password"}}}},
		"safe_observed_output_schema": map[string]any{}, "zone_selector": map[string]any{"mode": "all"},
		"capability_requirement": map[string]any{"all_of": []any{"kubernetes"}},
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/critical/managed-services/catalog/drafts/10000000-0000-4000-8000-000000000001/validate", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-user-id", "sre")
	request.Header.Set("x-session-proof-verified", "true")
	request.Header.Set("x-session-proof-challenge-id", "10000000-0000-4000-8000-000000000002")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", response.Code, response.Body.String())
	}
	if service.ValidateCalls != 0 {
		t.Fatal("literal Secret must be rejected at the handler boundary")
	}
}

func TestValidateDraftRejectsUnknownCustomerWidgetBeforeService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &mocks.RevisionService{}
	h := handler.NewRevisionHandler(service)
	router := gin.New()
	router.POST("/admin/critical/managed-services/catalog/drafts/:draft_id/validate", h.ValidateDraft)
	body := map[string]any{
		"expected_version":            1,
		"template_yaml":               "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: !aurora/component primary\nspec:\n  replicas: !aurora/param replicas\n",
		"contract_version":            "platform-form/v1",
		"component_contract":          []any{map[string]any{"id": "primary", "apply_order": 10, "delete_order": 10, "readiness": map[string]any{"type": "deployment_available"}}},
		"input_schema":                map[string]any{"fields": []any{map[string]any{"key": "replicas", "value_type": "INT64", "cardinality": "ONE", "required": true, "mutable": true, "min": 1, "max": 100}}},
		"ui_schema":                   map[string]any{"groups": []any{map[string]any{"key": "capacity", "order": 10, "label_i18n": map[string]any{"en": "Capacity"}}}, "fields": []any{map[string]any{"key": "replicas", "group": "capacity", "order": 10, "widget": "SLIDER", "label_i18n": map[string]any{"en": "Replicas"}}}},
		"safe_observed_output_schema": map[string]any{}, "zone_selector": map[string]any{"mode": "all"},
		"capability_requirement": map[string]any{"all_of": []any{"kubernetes"}},
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/critical/managed-services/catalog/drafts/10000000-0000-4000-8000-000000000001/validate", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-user-id", "sre")
	request.Header.Set("x-session-proof-verified", "true")
	request.Header.Set("x-session-proof-challenge-id", "10000000-0000-4000-8000-000000000002")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", response.Code, response.Body.String())
	}
	if service.ValidateCalls != 0 {
		t.Fatal("unknown widget must be rejected at the handler boundary")
	}
}

func TestValidateDraftSealsCurrentHashes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	draftID := uuid.MustParse("10000000-0000-4000-8000-000000000001")
	rowVersion := int64(1)
	service := &mocks.RevisionService{ValidateView: &entity.DraftView{
		ID: draftID, RowVersion: rowVersion, ValidatedRowVersion: &rowVersion,
		TemplateBundleSHA256: make([]byte, 32), ContractSHA256: make([]byte, 32),
	}}
	h := handler.NewRevisionHandler(service)
	router := gin.New()
	router.POST("/admin/critical/managed-services/catalog/drafts/:draft_id/validate", h.ValidateDraft)
	body := map[string]any{
		"expected_version":            1,
		"template_yaml":               "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: !aurora/component primary\nspec:\n  replicas: !aurora/param replicas\n",
		"contract_version":            "platform-form/v1",
		"component_contract":          []any{map[string]any{"id": "primary", "apply_order": 10, "delete_order": 10, "readiness": map[string]any{"type": "deployment_available"}}},
		"input_schema":                map[string]any{"fields": []any{map[string]any{"key": "replicas", "value_type": "INT64", "cardinality": "ONE", "required": true, "mutable": true, "min": 1, "max": 100}}},
		"ui_schema":                   map[string]any{"groups": []any{map[string]any{"key": "capacity", "order": 10, "label_i18n": map[string]any{"en": "Capacity"}}}, "fields": []any{map[string]any{"key": "replicas", "group": "capacity", "order": 10, "widget": "NUMBER", "label_i18n": map[string]any{"en": "Replicas"}}}},
		"safe_observed_output_schema": map[string]any{}, "zone_selector": map[string]any{"mode": "all"},
		"capability_requirement": map[string]any{"all_of": []any{"kubernetes"}},
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/critical/managed-services/catalog/drafts/"+draftID.String()+"/validate", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-user-id", "sre")
	request.Header.Set("x-session-proof-verified", "true")
	request.Header.Set("x-session-proof-challenge-id", "10000000-0000-4000-8000-000000000002")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if service.ValidateCalls != 1 || service.Validated == nil {
		t.Fatal("validated artifact must reach exactly one service workflow")
	}
	if len(service.Validated.TemplateBundleSHA256) != 32 || len(service.Validated.ContractSHA256) != 32 {
		t.Fatal("handler must seal exact bundle and contract hashes before service")
	}
	if service.Validated.AuditID != uuid.Nil {
		t.Fatal("handler must leave the audit identifier empty for the service workflow")
	}
}
