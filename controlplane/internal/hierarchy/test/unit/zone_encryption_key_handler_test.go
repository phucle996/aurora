package unit

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	entity "controlplane/internal/hierarchy/domain/entity"
	taxonomy "controlplane/internal/hierarchy/taxonomy"
	"controlplane/internal/hierarchy/test/mocks"
	hierarchyHandler "controlplane/internal/hierarchy/transport/http/handler"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	testZoneID  = "10000000-0000-7000-8000-000000000010"
	testKeyID   = "10000000-0000-7000-8000-000000000011"
	testProofID = "10000000-0000-7000-8000-000000000012"
)

func TestRegisterZoneEncryptionKeyFailsClosedWithoutCriticalProof(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &mocks.ZoneEncryptionKeyService{}
	h := hierarchyHandler.NewZoneEncryptionKeyHandler(service)
	router := gin.New()
	router.POST("/admin/critical/hierarchy/zones/:zone_id/encryption-keys", h.RegisterZoneEncryptionKey)

	request := httptest.NewRequest(http.MethodPost, "/admin/critical/hierarchy/zones/"+testZoneID+"/encryption-keys", bytes.NewBufferString(`{}`))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", response.Code, response.Body.String())
	}
	if service.RegisterCalls != 0 {
		t.Fatal("service must not observe request without critical proof")
	}
}

func TestRegisterZoneEncryptionKeyRejectsLowOrderPointAtHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &mocks.ZoneEncryptionKeyService{}
	h := hierarchyHandler.NewZoneEncryptionKeyHandler(service)
	router := gin.New()
	router.POST("/admin/critical/hierarchy/zones/:zone_id/encryption-keys", h.RegisterZoneEncryptionKey)
	body, _ := json.Marshal(map[string]string{"public_key": base64.StdEncoding.EncodeToString(make([]byte, 32))})
	request := httptest.NewRequest(http.MethodPost, "/admin/critical/hierarchy/zones/"+testZoneID+"/encryption-keys", bytes.NewReader(body))
	setCriticalHeaders(request)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", response.Code, response.Body.String())
	}
	if service.RegisterCalls != 0 {
		t.Fatal("invalid X25519 material must be rejected before service")
	}
}

func TestRegisterZoneEncryptionKeyMapsValidatedEntityAndGinHResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	publicKey := privateKey.PublicKey().Bytes()
	now := time.Now().UTC()
	service := &mocks.ZoneEncryptionKeyService{RegisterResult: &entity.RegisterZoneEncryptionKey{
		ID: uuid.MustParse(testKeyID), ZoneID: uuid.MustParse(testZoneID), PublicKey: publicKey,
		Fingerprint: bytes.Repeat([]byte{0x31}, 32), Algorithm: entity.ZoneEncryptionKeyAlgorithm,
		Status: entity.ZoneEncryptionKeyStatusStaged, RegisteredBy: "sre", CreatedAt: now, UpdatedAt: now,
	}}
	h := hierarchyHandler.NewZoneEncryptionKeyHandler(service)
	router := gin.New()
	router.POST("/admin/critical/hierarchy/zones/:zone_id/encryption-keys", h.RegisterZoneEncryptionKey)
	body, _ := json.Marshal(map[string]string{"public_key": base64.StdEncoding.EncodeToString(publicKey)})
	request := httptest.NewRequest(http.MethodPost, "/admin/critical/hierarchy/zones/"+testZoneID+"/encryption-keys", bytes.NewReader(body))
	setCriticalHeaders(request)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", response.Code, response.Body.String())
	}
	if service.RegisterCalls != 1 || service.Registered == nil {
		t.Fatal("validated request must reach exactly one service workflow")
	}
	if service.Registered.ID != uuid.Nil {
		t.Fatal("handler must leave system key UUID empty for service")
	}
	if !bytes.Equal(service.Registered.PublicKey, publicKey) {
		t.Fatal("handler must pass exact decoded public key bytes")
	}
	var envelope map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data, ok := envelope["data"].(map[string]any)
	if !ok || data["public_key"] != base64.StdEncoding.EncodeToString(publicKey) || data["status"] != "staged" {
		t.Fatalf("unexpected gin.H response: %#v", envelope)
	}
	if _, exists := data["private_key"]; exists {
		t.Fatal("private key field must never cross the HTTP boundary")
	}
}

func TestListZoneEncryptionKeysReturnsPublicInventory(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &mocks.ZoneEncryptionKeyService{ListResult: []entity.ListZoneEncryptionKeys{{
		ID: uuid.MustParse(testKeyID), ZoneID: uuid.MustParse(testZoneID), PublicKey: bytes.Repeat([]byte{0x22}, 32),
		Fingerprint: bytes.Repeat([]byte{0x33}, 32), Algorithm: entity.ZoneEncryptionKeyAlgorithm,
		Status: entity.ZoneEncryptionKeyStatusActive,
	}}}
	h := hierarchyHandler.NewZoneEncryptionKeyHandler(service)
	router := gin.New()
	router.GET("/admin/hierarchy/zones/:zone_id/encryption-keys", h.ListZoneEncryptionKeys)
	request := httptest.NewRequest(http.MethodGet, "/admin/hierarchy/zones/"+testZoneID+"/encryption-keys", nil)
	request.Header.Set("x-user-id", "sre")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK || service.ListCalls != 1 {
		t.Fatalf("expected list success, got %d: %s", response.Code, response.Body.String())
	}
	if service.Listed == nil || service.Listed.Limit != 50 {
		t.Fatal("list handler must enforce a bounded default page")
	}
}

func TestActivateZoneEncryptionKeyMapsStateConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &mocks.ZoneEncryptionKeyService{ActivateErr: taxonomy.ErrInvalidTransition}
	h := hierarchyHandler.NewZoneEncryptionKeyHandler(service)
	router := gin.New()
	router.POST("/admin/critical/hierarchy/zones/:zone_id/encryption-keys/:key_id/activate", h.ActivateZoneEncryptionKey)
	request := httptest.NewRequest(http.MethodPost, "/admin/critical/hierarchy/zones/"+testZoneID+"/encryption-keys/"+testKeyID+"/activate", nil)
	setCriticalHeaders(request)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusConflict || service.ActivateCalls != 1 {
		t.Fatalf("expected 409, got %d: %s", response.Code, response.Body.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode conflict response: %v", err)
	}
	if envelope["error"] != "conflict" {
		t.Fatalf("response must expose only generic HTTP error, got %#v", envelope["error"])
	}
}

func TestRetireZoneEncryptionKeyMapsNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &mocks.ZoneEncryptionKeyService{RetireErr: taxonomy.ErrNotFound}
	h := hierarchyHandler.NewZoneEncryptionKeyHandler(service)
	router := gin.New()
	router.POST("/admin/critical/hierarchy/zones/:zone_id/encryption-keys/:key_id/retire", h.RetireZoneEncryptionKey)
	request := httptest.NewRequest(http.MethodPost, "/admin/critical/hierarchy/zones/"+testZoneID+"/encryption-keys/"+testKeyID+"/retire", nil)
	setCriticalHeaders(request)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound || service.RetireCalls != 1 {
		t.Fatalf("expected 404, got %d: %s", response.Code, response.Body.String())
	}
}

func setCriticalHeaders(request *http.Request) {
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-user-id", "sre")
	request.Header.Set("x-session-proof-verified", "true")
	request.Header.Set("x-session-proof-challenge-id", testProofID)
}
