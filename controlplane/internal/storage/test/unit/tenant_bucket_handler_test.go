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

type tenantBucketServiceStub struct {
	storageSvcInterface.TenantBucketService
	createdResult    *storageEntity.CreatedBucketResult
	createErr        error
	bucket           *storageEntity.TenantBucket
	getErr           error
	buckets          []*storageEntity.TenantBucket
	listErr          error
	updateQuotaErr   error
	updatedVer       bool
	updateVerErr     error
	updatedLifecycle []storageEntity.BucketLifecycleRule
	lifecycleErr     error
	deleteErr        error
}

func (s *tenantBucketServiceStub) CreateBucketForTenant(_ context.Context, param *storageEntity.CreateTenantBucket) (*storageEntity.CreatedBucketResult, error) {
	if s.createErr != nil {
		return nil, s.createErr
	}
	if s.createdResult != nil {
		return s.createdResult, nil
	}
	return &storageEntity.CreatedBucketResult{
		BucketID:     uuid.New(),
		BucketName:   "tn-12345678-" + param.Name,
		CredentialID: uuid.New(),
		AccessKey:    "AKIA...",
		SecretKey:    "SECRET...",
		Policy:       "{}",
	}, nil
}

func (s *tenantBucketServiceStub) GetBucket(_ context.Context, bucketID uuid.UUID, workspaceID uuid.UUID, tenantID uuid.UUID, userID uuid.UUID, zoneID uuid.UUID) (*storageEntity.TenantBucket, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.bucket != nil {
		return s.bucket, nil
	}
	return &storageEntity.TenantBucket{
		ID:                 bucketID,
		Name:               "tn-12345678-test-bucket",
		Status:             storageEntity.BucketStatusReady,
		WorkspaceID:        workspaceID,
		TenantID:           tenantID,
		ZoneID:             zoneID,
		CapacityQuotaBytes: 107374182400,
		UsedBytes:          1048576,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}, nil
}

func (s *tenantBucketServiceStub) ListBuckets(_ context.Context, workspaceID uuid.UUID, tenantID uuid.UUID, userID uuid.UUID, zoneID uuid.UUID) ([]*storageEntity.TenantBucket, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	if s.buckets != nil {
		return s.buckets, nil
	}
	return []*storageEntity.TenantBucket{
		{
			ID:                 uuid.New(),
			Name:               "tn-12345678-b1",
			WorkspaceID:        workspaceID,
			TenantID:           tenantID,
			ZoneID:             zoneID,
			CapacityQuotaBytes: 107374182400,
			CreatedAt:          time.Now(),
			UpdatedAt:          time.Now(),
		},
	}, nil
}

func (s *tenantBucketServiceStub) UpdateBucketQuota(_ context.Context, _ *storageEntity.UpdateTenantBucketQuota) error {
	return s.updateQuotaErr
}

func (s *tenantBucketServiceStub) UpdateBucketVersioning(_ context.Context, param *storageEntity.UpdateTenantBucketVersioning) (*storageEntity.TenantBucket, error) {
	if s.updateVerErr != nil {
		return nil, s.updateVerErr
	}
	s.updatedVer = param.VersioningEnabled
	if s.bucket != nil {
		s.bucket.VersioningEnabled = param.VersioningEnabled
		return s.bucket, nil
	}
	return &storageEntity.TenantBucket{
		ID:                param.BucketID,
		Name:              "tn-test",
		VersioningEnabled: param.VersioningEnabled,
	}, nil
}

func (s *tenantBucketServiceStub) GetBucketLifecycle(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ uuid.UUID, _ uuid.UUID, _ uuid.UUID) ([]storageEntity.BucketLifecycleRule, error) {
	if s.bucket != nil {
		return s.bucket.LifecycleRules, nil
	}
	return nil, nil
}

func (s *tenantBucketServiceStub) UpdateBucketLifecycle(_ context.Context, param *storageEntity.UpdateTenantBucketLifecycle) (*storageEntity.TenantBucket, error) {
	if s.lifecycleErr != nil {
		return nil, s.lifecycleErr
	}
	s.updatedLifecycle = param.Rules
	if s.bucket != nil {
		s.bucket.LifecycleRules = param.Rules
		return s.bucket, nil
	}
	return &storageEntity.TenantBucket{
		ID:             param.BucketID,
		Name:           "tn-test",
		LifecycleRules: param.Rules,
	}, nil
}

func (s *tenantBucketServiceStub) DeleteBucket(_ context.Context, _ *storageEntity.DeleteTenantBucket) error {
	return s.deleteErr
}

func setupTenantBucketTestRouter(svc storageSvcInterface.TenantBucketService, userID, workspaceID, tenantID, zoneID uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := storageHandler.NewTenantBucketHandler(svc)
	router := gin.New()

	router.Use(func(c *gin.Context) {
		c.Set(pkgcontext.CtxUserID, userID)
		c.Set(pkgcontext.CtxWorkspaceID, workspaceID)
		c.Set(pkgcontext.CtxTenantID, tenantID)
		c.Set(pkgcontext.CtxZoneID, zoneID)
		c.Next()
	})

	router.POST("/api/v1/tenant/storage/buckets", handler.Create)
	router.GET("/api/v1/tenant/storage/buckets", handler.List)
	router.GET("/api/v1/tenant/storage/buckets/:id", handler.Get)
	router.PATCH("/api/v1/tenant/storage/buckets/:id/quota", handler.UpdateQuota)
	router.PATCH("/api/v1/tenant/storage/buckets/:id/versioning", handler.UpdateVersioning)
	router.GET("/api/v1/tenant/storage/buckets/:id/lifecycle", handler.GetLifecycle)
	router.PUT("/api/v1/tenant/storage/buckets/:id/lifecycle", handler.UpdateLifecycle)
	router.DELETE("/api/v1/tenant/storage/buckets/:id", handler.Delete)

	return router
}

func TestTenantBucketCreate_Success(t *testing.T) {
	userID, workspaceID, tenantID, zoneID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	svc := &tenantBucketServiceStub{}
	router := setupTenantBucketTestRouter(svc, userID, workspaceID, tenantID, zoneID)

	body := `{"name": "company-assets", "quota_bytes": 107374182400}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tenant/storage/buckets", bytes.NewBufferString(body))
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
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected object data in response, got %#v", resp["data"])
	}
	for _, field := range []string{"bucket_id", "bucket_name", "credential_id", "access_key", "secret_key", "policy"} {
		if data[field] == nil {
			t.Fatalf("expected neutral create field %q, got %#v", field, data)
		}
	}
	if data["bucket"] != nil || data["credential"] != nil {
		t.Fatalf("tenant response must match the neutral create contract: %#v", data)
	}
}

func TestTenantBucketCreate_Conflict(t *testing.T) {
	userID, workspaceID, tenantID, zoneID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	svc := &tenantBucketServiceStub{createErr: storageTaxonomy.ErrAlreadyExists}
	router := setupTenantBucketTestRouter(svc, userID, workspaceID, tenantID, zoneID)

	body := `{"name": "company-assets", "quota_bytes": 107374182400}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tenant/storage/buckets", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTenantBucketGet_Success(t *testing.T) {
	userID, workspaceID, tenantID, zoneID, bucketID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	svc := &tenantBucketServiceStub{}
	router := setupTenantBucketTestRouter(svc, userID, workspaceID, tenantID, zoneID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tenant/storage/buckets/"+bucketID.String(), nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Data struct {
			Status      string `json:"status"`
			WorkspaceID string `json:"workspace_id"`
			UsedMB      string `json:"used_mb"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if response.Data.Status != storageEntity.BucketStatusReady || response.Data.WorkspaceID != workspaceID.String() || response.Data.UsedMB != "1.000000" {
		t.Fatalf("neutral bucket detail fields mismatch: %#v", response.Data)
	}
}

func TestTenantBucketGet_NotFound(t *testing.T) {
	userID, workspaceID, tenantID, zoneID, bucketID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	svc := &tenantBucketServiceStub{getErr: storageTaxonomy.ErrNotFound}
	router := setupTenantBucketTestRouter(svc, userID, workspaceID, tenantID, zoneID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tenant/storage/buckets/"+bucketID.String(), nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTenantBucketList_Success(t *testing.T) {
	userID, workspaceID, tenantID, zoneID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	svc := &tenantBucketServiceStub{}
	router := setupTenantBucketTestRouter(svc, userID, workspaceID, tenantID, zoneID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tenant/storage/buckets", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTenantBucketUpdateQuota_Success(t *testing.T) {
	userID, workspaceID, tenantID, zoneID, bucketID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	svc := &tenantBucketServiceStub{}
	router := setupTenantBucketTestRouter(svc, userID, workspaceID, tenantID, zoneID)

	body := `{"quota_bytes": 214748364800}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/tenant/storage/buckets/"+bucketID.String()+"/quota", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTenantBucketUpdateVersioning_Success(t *testing.T) {
	userID, workspaceID, tenantID, zoneID, bucketID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	svc := &tenantBucketServiceStub{}
	router := setupTenantBucketTestRouter(svc, userID, workspaceID, tenantID, zoneID)

	body := `{"versioning_enabled": true}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/tenant/storage/buckets/"+bucketID.String()+"/versioning", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}
	if !svc.updatedVer {
		t.Fatalf("expected versioning_enabled to be updated to true")
	}
}

func TestTenantBucketLifecycle_VersioningRequiredInvariant(t *testing.T) {
	userID, workspaceID, tenantID, zoneID, bucketID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	svc := &tenantBucketServiceStub{lifecycleErr: storageTaxonomy.ErrVersioningRequired}
	router := setupTenantBucketTestRouter(svc, userID, workspaceID, tenantID, zoneID)

	body := `{"rules": [{"id": "rule-1", "enabled": true, "prefix": "logs/", "expiration_days": 30, "noncurrent_version_expiration_days": 10, "abort_incomplete_multipart_upload_days": 7}]}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/tenant/storage/buckets/"+bucketID.String()+"/lifecycle", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request on ErrVersioningRequired, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTenantBucketDelete_Success(t *testing.T) {
	userID, workspaceID, tenantID, zoneID, bucketID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	svc := &tenantBucketServiceStub{}
	router := setupTenantBucketTestRouter(svc, userID, workspaceID, tenantID, zoneID)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/tenant/storage/buckets/"+bucketID.String(), nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}
}
