package storageHandler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	storageEntity "controlplane/internal/storage/domain/entity"
	storageSvcInterface "controlplane/internal/storage/domain/service"
	storageTaxonomy "controlplane/internal/storage/taxonomy"
	storageDto "controlplane/internal/storage/transport/http/dto"
	apires "controlplane/pkg/apires"
	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TenantBucketHandler xử lý các HTTP request quản trị Bucket cho Doanh nghiệp (Tenant).
type TenantBucketHandler struct {
	tenantSvc storageSvcInterface.TenantBucketService
}

// NewTenantBucketHandler khởi tạo controller xử lý các endpoint Bucket cho Tenant.
func NewTenantBucketHandler(
	tenantSvc storageSvcInterface.TenantBucketService,
) *TenantBucketHandler {
	return &TenantBucketHandler{
		tenantSvc: tenantSvc,
	}
}

// Create tiếp nhận yêu cầu tạo mới bucket cho tenant workspace.
func (h *TenantBucketHandler) Create(c *gin.Context) {
	const op = "storage.tenant_bucket.create"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	workspaceID, ok := pkgcontext.GetWorkspaceID(c, op)
	if !ok {
		return
	}

	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}

	tenantID, ok := pkgcontext.GetTenantID(c, op)
	if !ok {
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 65536)
	var req storageDto.CreateTenantBucketRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		apires.RespondBadRequest(c, "invalid request body")
		return
	}

	bucketName := strings.TrimSpace(req.Name)
	if bucketName == "" {
		apires.RespondBadRequest(c, "bucket name cannot be empty")
		return
	}

	param := &storageEntity.CreateTenantBucket{
		Name:                 bucketName,
		WorkspaceID:          workspaceID,
		ZoneID:               zoneID,
		TenantID:             tenantID,
		CapacityQuotaBytes:   req.QuotaBytes,
		UserID:               userID,
		EncryptEnabled:       req.EncryptEnabled,
		VersioningEnabled:    req.VersioningEnabled,
		ObjectLockingEnabled: req.ObjectLockingEnabled,
		ReplicationEnabled:   req.ReplicationEnabled,
		RetentionDays:        req.RetentionDays,
		LegalHoldEnabled:     req.LegalHoldEnabled,
		Tags:                 req.Tags,
	}

	createResult, createErr := h.tenantSvc.CreateBucketForTenant(ctx, param)
	if createErr != nil {
		switch {
		case errors.Is(createErr, storageTaxonomy.ErrAlreadyExists):
			logger.HandlerWarn(c, op, createErr, "bucket name conflict: "+bucketName)
			apires.RespondConflict(c, "bucket name already exists")
		case errors.Is(createErr, storageTaxonomy.ErrInvalidBucketName):
			logger.HandlerWarn(c, op, createErr, "invalid bucket name format")
			apires.RespondBadRequest(c, "invalid bucket name format")
		case errors.Is(createErr, storageTaxonomy.ErrCommercialAdmissionDenied):
			apires.RespondServiceUnavailable(c, "STORAGE_WALLET_ADMISSION_UNAVAILABLE")
		case errors.Is(createErr, storageTaxonomy.ErrNotFound):
			apires.RespondForbidden(c, "workspace does not exist in this tenant or user is not active")
		default:
			logger.HandlerError(c, op, createErr)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	apires.RespondCreated(c, gin.H{
		"bucket": gin.H{
			"id":                   createResult.BucketID.String(),
			"name":                 createResult.BucketName,
			"workspace_id":         workspaceID.String(),
			"zone_id":              zoneID.String(),
			"tenant_id":            tenantID.String(),
			"capacity_quota_bytes": req.QuotaBytes,
			"used_bytes":           0,
			"versioning_enabled":   req.VersioningEnabled,
			"lifecycle_rules":      []storageEntity.BucketLifecycleRule{},
		},
		"credential": gin.H{
			"id":         createResult.CredentialID.String(),
			"access_key": createResult.AccessKey,
			"secret_key": createResult.SecretKey,
			"policy":     createResult.Policy,
		},
	}, "tenant bucket created successfully")
}

// Get truy vấn thông tin chi tiết một bucket của tenant.
func (h *TenantBucketHandler) Get(c *gin.Context) {
	const op = "storage.tenant_bucket.get"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	workspaceID, ok := pkgcontext.GetWorkspaceID(c, op)
	if !ok {
		return
	}

	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}

	tenantID, ok := pkgcontext.GetTenantID(c, op)
	if !ok {
		return
	}

	idStr := c.Param("id")
	bucketID, err := uuid.Parse(idStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	bucket, err := h.tenantSvc.GetBucket(ctx, bucketID, workspaceID, tenantID, userID, zoneID)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			apires.RespondNotFound(c, "bucket not found")
		} else {
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	lifecycleRules := bucket.LifecycleRules
	if lifecycleRules == nil {
		lifecycleRules = []storageEntity.BucketLifecycleRule{}
	}

	apires.RespondSuccess(c, gin.H{
		"id":                     bucket.ID.String(),
		"name":                   bucket.Name,
		"workspace_id":           bucket.WorkspaceID.String(),
		"zone_id":                bucket.ZoneID.String(),
		"tenant_id":              bucket.TenantID.String(),
		"capacity_quota_bytes":   bucket.CapacityQuotaBytes,
		"used_bytes":             bucket.UsedBytes,
		"used_bytes_megabytes":   formatUsedMegabytes(bucket.UsedBytes),
		"encrypt_enabled":        bucket.EncryptEnabled,
		"versioning_enabled":     bucket.VersioningEnabled,
		"object_locking_enabled": bucket.ObjectLockingEnabled,
		"replication_enabled":    bucket.ReplicationEnabled,
		"retention_days":         bucket.RetentionDays,
		"legal_hold_enabled":     bucket.LegalHoldEnabled,
		"tags":                   bucket.Tags,
		"lifecycle_rules":        lifecycleRules,
		"created_at":             bucket.CreatedAt.Format(time.RFC3339),
		"updated_at":             bucket.UpdatedAt.Format(time.RFC3339),
	}, "tenant bucket retrieved successfully")
}

// List truy vấn danh sách bucket trong tenant workspace.
func (h *TenantBucketHandler) List(c *gin.Context) {
	const op = "storage.tenant_bucket.list"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	workspaceID, ok := pkgcontext.GetWorkspaceID(c, op)
	if !ok {
		return
	}

	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}

	tenantID, ok := pkgcontext.GetTenantID(c, op)
	if !ok {
		return
	}

	buckets, err := h.tenantSvc.ListBuckets(ctx, workspaceID, tenantID, userID, zoneID)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	items := make([]gin.H, 0, len(buckets))
	for _, b := range buckets {
		lifecycleRules := b.LifecycleRules
		if lifecycleRules == nil {
			lifecycleRules = []storageEntity.BucketLifecycleRule{}
		}
		items = append(items, gin.H{
			"id":                     b.ID.String(),
			"name":                   b.Name,
			"workspace_id":           b.WorkspaceID.String(),
			"zone_id":                b.ZoneID.String(),
			"tenant_id":              b.TenantID.String(),
			"capacity_quota_bytes":   b.CapacityQuotaBytes,
			"used_bytes":             b.UsedBytes,
			"used_bytes_megabytes":   formatUsedMegabytes(b.UsedBytes),
			"encrypt_enabled":        b.EncryptEnabled,
			"versioning_enabled":     b.VersioningEnabled,
			"object_locking_enabled": b.ObjectLockingEnabled,
			"replication_enabled":    b.ReplicationEnabled,
			"retention_days":         b.RetentionDays,
			"legal_hold_enabled":     b.LegalHoldEnabled,
			"tags":                   b.Tags,
			"lifecycle_rules":        lifecycleRules,
			"created_at":             b.CreatedAt.Format(time.RFC3339),
			"updated_at":             b.UpdatedAt.Format(time.RFC3339),
		})
	}

	apires.RespondSuccess(c, items, "tenant buckets retrieved successfully")
}

// UpdateQuota cập nhật hạn mức dung lượng lưu trữ của bucket.
func (h *TenantBucketHandler) UpdateQuota(c *gin.Context) {
	const op = "storage.tenant_bucket.update_quota"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	workspaceID, ok := pkgcontext.GetWorkspaceID(c, op)
	if !ok {
		return
	}

	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}

	tenantID, ok := pkgcontext.GetTenantID(c, op)
	if !ok {
		return
	}

	idStr := c.Param("id")
	bucketID, err := uuid.Parse(idStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 65536)
	var req storageDto.UpdateTenantQuotaRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		apires.RespondBadRequest(c, "invalid request body")
		return
	}
	if req.QuotaBytes <= 0 {
		apires.RespondBadRequest(c, "quota_bytes must be positive")
		return
	}

	param := &storageEntity.UpdateTenantBucketQuota{
		BucketID:    bucketID,
		WorkspaceID: workspaceID,
		TenantID:    tenantID,
		UserID:      userID,
		ZoneID:      zoneID,
		QuotaBytes:  req.QuotaBytes,
	}

	if err := h.tenantSvc.UpdateBucketQuota(ctx, param); err != nil {
		switch {
		case errors.Is(err, storageTaxonomy.ErrNotFound):
			apires.RespondNotFound(c, "bucket not found")
		case errors.Is(err, storageTaxonomy.ErrResizeLimitTooLow):
			apires.RespondBadRequest(c, "quota leaves less than one GiB free")
		case errors.Is(err, storageTaxonomy.ErrCommercialAdmissionDenied):
			apires.RespondServiceUnavailable(c, "STORAGE_WALLET_ADMISSION_UNAVAILABLE")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	apires.RespondSuccess(c, nil, "bucket quota updated")
}

// UpdateVersioning cập nhật trạng thái versioning của bucket.
func (h *TenantBucketHandler) UpdateVersioning(c *gin.Context) {
	const op = "storage.tenant_bucket.update_versioning"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	workspaceID, ok := pkgcontext.GetWorkspaceID(c, op)
	if !ok {
		return
	}

	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}

	tenantID, ok := pkgcontext.GetTenantID(c, op)
	if !ok {
		return
	}

	idStr := c.Param("id")
	bucketID, err := uuid.Parse(idStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 65536)
	var req storageDto.UpdateTenantBucketVersioningRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		apires.RespondBadRequest(c, "invalid request body")
		return
	}

	param := &storageEntity.UpdateTenantBucketVersioning{
		BucketID:          bucketID,
		WorkspaceID:       workspaceID,
		TenantID:          tenantID,
		UserID:            userID,
		ZoneID:            zoneID,
		VersioningEnabled: req.VersioningEnabled,
	}

	bucket, err := h.tenantSvc.UpdateBucketVersioning(ctx, param)
	if err != nil {
		switch {
		case errors.Is(err, storageTaxonomy.ErrNotFound):
			apires.RespondNotFound(c, "bucket not found")
		case errors.Is(err, storageTaxonomy.ErrCommercialAdmissionDenied):
			apires.RespondServiceUnavailable(c, "STORAGE_WALLET_ADMISSION_UNAVAILABLE")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{
		"id":                 bucket.ID.String(),
		"name":               bucket.Name,
		"versioning_enabled": bucket.VersioningEnabled,
	}, "tenant bucket versioning updated")
}

// GetLifecycle lấy danh sách lifecycle rules của bucket.
func (h *TenantBucketHandler) GetLifecycle(c *gin.Context) {
	const op = "storage.tenant_bucket.get_lifecycle"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	workspaceID, ok := pkgcontext.GetWorkspaceID(c, op)
	if !ok {
		return
	}

	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}

	tenantID, ok := pkgcontext.GetTenantID(c, op)
	if !ok {
		return
	}

	idStr := c.Param("id")
	bucketID, err := uuid.Parse(idStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	rules, err := h.tenantSvc.GetBucketLifecycle(ctx, bucketID, workspaceID, tenantID, userID, zoneID)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			apires.RespondNotFound(c, "bucket not found")
		} else {
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	dtoRules := make([]storageDto.TenantBucketLifecycleRuleDTO, len(rules))
	for i, r := range rules {
		dtoRules[i] = storageDto.TenantBucketLifecycleRuleDTO{
			ID:                                 r.ID,
			Enabled:                            r.Enabled,
			Prefix:                             r.Prefix,
			ExpirationDays:                     r.ExpirationDays,
			NoncurrentVersionExpirationDays:    r.NoncurrentVersionExpirationDays,
			AbortIncompleteMultipartUploadDays: r.AbortIncompleteMultipartUploadDays,
		}
	}

	apires.RespondSuccess(c, storageDto.TenantBucketLifecycleResponse{Rules: dtoRules}, "lifecycle rules retrieved successfully")
}

// UpdateLifecycle cập nhật danh sách lifecycle rules của bucket.
func (h *TenantBucketHandler) UpdateLifecycle(c *gin.Context) {
	const op = "storage.tenant_bucket.update_lifecycle"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	workspaceID, ok := pkgcontext.GetWorkspaceID(c, op)
	if !ok {
		return
	}

	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}

	tenantID, ok := pkgcontext.GetTenantID(c, op)
	if !ok {
		return
	}

	idStr := c.Param("id")
	bucketID, err := uuid.Parse(idStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 65536)
	var req storageDto.UpdateTenantBucketLifecycleRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		apires.RespondBadRequest(c, "invalid request body")
		return
	}

	domainRules := make([]storageEntity.BucketLifecycleRule, len(req.Rules))
	for i, r := range req.Rules {
		domainRules[i] = storageEntity.BucketLifecycleRule{
			ID:                                 r.ID,
			Enabled:                            r.Enabled,
			Prefix:                             r.Prefix,
			ExpirationDays:                     r.ExpirationDays,
			NoncurrentVersionExpirationDays:    r.NoncurrentVersionExpirationDays,
			AbortIncompleteMultipartUploadDays: r.AbortIncompleteMultipartUploadDays,
		}
	}

	param := &storageEntity.UpdateTenantBucketLifecycle{
		BucketID:    bucketID,
		WorkspaceID: workspaceID,
		TenantID:    tenantID,
		UserID:      userID,
		ZoneID:      zoneID,
		Rules:       domainRules,
	}

	bucket, err := h.tenantSvc.UpdateBucketLifecycle(ctx, param)
	if err != nil {
		switch {
		case errors.Is(err, storageTaxonomy.ErrNotFound):
			apires.RespondNotFound(c, "bucket not found")
		case errors.Is(err, storageTaxonomy.ErrVersioningRequired):
			apires.RespondBadRequest(c, "noncurrent version expiration requires bucket versioning to be enabled")
		case errors.Is(err, storageTaxonomy.ErrCommercialAdmissionDenied):
			apires.RespondServiceUnavailable(c, "STORAGE_WALLET_ADMISSION_UNAVAILABLE")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{
		"id":              bucket.ID.String(),
		"name":            bucket.Name,
		"lifecycle_rules": bucket.LifecycleRules,
	}, "tenant bucket lifecycle rules updated")
}

// Delete tiếp nhận yêu cầu xóa bucket.
func (h *TenantBucketHandler) Delete(c *gin.Context) {
	const op = "storage.tenant_bucket.delete"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	workspaceID, ok := pkgcontext.GetWorkspaceID(c, op)
	if !ok {
		return
	}

	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}

	tenantID, ok := pkgcontext.GetTenantID(c, op)
	if !ok {
		return
	}

	idStr := c.Param("id")
	bucketID, err := uuid.Parse(idStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	param := &storageEntity.DeleteTenantBucket{
		BucketID:    bucketID,
		WorkspaceID: workspaceID,
		TenantID:    tenantID,
		ZoneID:      zoneID,
		UserID:      userID,
	}

	if err := h.tenantSvc.DeleteBucket(ctx, param); err != nil {
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			apires.RespondNotFound(c, "bucket not found")
		} else {
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	apires.RespondSuccess(c, nil, "tenant bucket deletion initiated")
}
