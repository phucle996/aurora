package unit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	storageEntity "controlplane/internal/storage/domain/entity"
	storageSvcInterface "controlplane/internal/storage/domain/service"
	storageTaxonomy "controlplane/internal/storage/taxonomy"
	storageHandler "controlplane/internal/storage/transport/http/handler"
	pkgcontext "controlplane/pkg/context"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type tenantCredentialServiceStub struct {
	storageSvcInterface.TenantCredentialService
	createdCred *storageEntity.CreatedTenantCredential
	createErr   error
	creds       []*storageEntity.TenantCredential
	listErr     error
	deleteErr   error
}

func (s *tenantCredentialServiceStub) CreateCredential(_ context.Context, param *storageEntity.CreateTenantCredential) (*storageEntity.CreatedTenantCredential, error) {
	if s.createErr != nil {
		return nil, s.createErr
	}
	if s.createdCred != nil {
		return s.createdCred, nil
	}
	return &storageEntity.CreatedTenantCredential{
		ID:        uuid.New(),
		BucketID:  param.BucketID,
		AccessKey: "AKIA...",
		SecretKey: "SECRET...",
		Policy:    param.Policy,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil
}

func (s *tenantCredentialServiceStub) ListCredentials(_ context.Context, bucketID uuid.UUID, _ uuid.UUID, _ uuid.UUID, _ uuid.UUID, _ uuid.UUID) ([]*storageEntity.TenantCredential, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	if s.creds != nil {
		return s.creds, nil
	}
	return []*storageEntity.TenantCredential{
		{
			ID:        uuid.New(),
			BucketID:  bucketID,
			AccessKey: "AKIA...",
			Policy:    "{}",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}, nil
}

func (s *tenantCredentialServiceStub) DeleteCredential(_ context.Context, _ *storageEntity.DeleteTenantCredential) error {
	return s.deleteErr
}

func setupTenantCredentialTestRouter(svc storageSvcInterface.TenantCredentialService, userID, workspaceID, tenantID, zoneID uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := storageHandler.NewTenantCredentialHandler(svc)
	router := gin.New()

	router.Use(func(c *gin.Context) {
		c.Set(pkgcontext.CtxUserID, userID)
		c.Set(pkgcontext.CtxWorkspaceID, workspaceID)
		c.Set(pkgcontext.CtxTenantID, tenantID)
		c.Set(pkgcontext.CtxZoneID, zoneID)
		c.Next()
	})

	router.POST("/api/v1/tenant/storage/buckets/:id/credentials", handler.Create)
	router.GET("/api/v1/tenant/storage/buckets/:id/credentials", handler.List)
	router.DELETE("/api/v1/tenant/storage/buckets/:id/credentials/:credential_id", handler.Delete)

	return router
}

func TestTenantCredentialCreate_Success(t *testing.T) {
	userID, workspaceID, tenantID, zoneID, bucketID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	svc := &tenantCredentialServiceStub{}
	router := setupTenantCredentialTestRouter(svc, userID, workspaceID, tenantID, zoneID)

	body := `{"policy": "{}"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tenant/storage/buckets/"+bucketID.String()+"/credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp["data"] == nil {
		t.Fatalf("expected data in response, got nil")
	}
}

func TestTenantCredentialCreate_BucketNotFound(t *testing.T) {
	userID, workspaceID, tenantID, zoneID, bucketID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	svc := &tenantCredentialServiceStub{createErr: storageTaxonomy.ErrNotFound}
	router := setupTenantCredentialTestRouter(svc, userID, workspaceID, tenantID, zoneID)

	body := `{"policy": "{}"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tenant/storage/buckets/"+bucketID.String()+"/credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTenantCredentialList_Success(t *testing.T) {
	userID, workspaceID, tenantID, zoneID, bucketID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	svc := &tenantCredentialServiceStub{}
	router := setupTenantCredentialTestRouter(svc, userID, workspaceID, tenantID, zoneID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tenant/storage/buckets/"+bucketID.String()+"/credentials", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTenantCredentialDelete_Success(t *testing.T) {
	userID, workspaceID, tenantID, zoneID, bucketID, credID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	svc := &tenantCredentialServiceStub{}
	router := setupTenantCredentialTestRouter(svc, userID, workspaceID, tenantID, zoneID)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/tenant/storage/buckets/"+bucketID.String()+"/credentials/"+credID.String(), nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}
}
