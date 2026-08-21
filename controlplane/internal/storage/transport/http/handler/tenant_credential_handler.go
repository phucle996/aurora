package storageHandler

import (
	"context"
	"errors"
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

// TenantCredentialHandler quản lý các request HTTP tương tác với Access Keys của MinIO cho Tenant.
type TenantCredentialHandler struct {
	tenantSvc storageSvcInterface.TenantCredentialService
}

// NewTenantCredentialHandler khởi tạo controller quản lý key credentials cho Tenant.
func NewTenantCredentialHandler(
	tenantSvc storageSvcInterface.TenantCredentialService,
) *TenantCredentialHandler {
	return &TenantCredentialHandler{
		tenantSvc: tenantSvc,
	}
}

// Create tạo mới Access Key trong bucket của tenant.
func (h *TenantCredentialHandler) Create(c *gin.Context) {
	const op = "storage.tenant_credential.create"
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

	bucketIDStr := strings.TrimSpace(c.Param("id"))
	bucketID, err := uuid.Parse(bucketIDStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	var req storageDto.CreateTenantCredentialRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// Optional body
		req.Policy = ""
	}

	param := &storageEntity.CreateTenantCredential{
		BucketID:    bucketID,
		Policy:      req.Policy,
		TenantID:    tenantID,
		UserID:      userID,
		WorkspaceID: workspaceID,
		ZoneID:      zoneID,
	}

	cred, err := h.tenantSvc.CreateCredential(ctx, param)
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

	res := gin.H{
		"id":         cred.ID.String(),
		"access_key": cred.AccessKey,
		"secret_key": cred.SecretKey,
		"policy":     cred.Policy,
		"created_at": cred.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at": cred.UpdatedAt.UTC().Format(time.RFC3339),
	}
	apires.RespondCreated(c, res, "tenant credential created successfully")
}

// List trả về danh sách các access credentials của bucket thuộc tenant.
func (h *TenantCredentialHandler) List(c *gin.Context) {
	const op = "storage.tenant_credential.list"
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

	bucketIDStr := strings.TrimSpace(c.Param("id"))
	bucketID, err := uuid.Parse(bucketIDStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	creds, err := h.tenantSvc.ListCredentials(ctx, bucketID, workspaceID, tenantID, userID, zoneID)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	var res []gin.H
	for _, cred := range creds {
		res = append(res, gin.H{
			"id":         cred.ID.String(),
			"access_key": cred.AccessKey,
			"policy":     cred.Policy,
			"created_at": cred.CreatedAt.UTC().Format(time.RFC3339),
			"updated_at": cred.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	apires.RespondSuccess(c, res, "tenant credentials retrieved successfully")
}

// Delete xóa bỏ access credential của tenant bucket.
func (h *TenantCredentialHandler) Delete(c *gin.Context) {
	const op = "storage.tenant_credential.delete"
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

	bucketIDStr := strings.TrimSpace(c.Param("id"))
	bucketID, err := uuid.Parse(bucketIDStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	credIDStr := strings.TrimSpace(c.Param("credential_id"))
	credID, err := uuid.Parse(credIDStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid credential id format")
		return
	}

	var req storageDto.DeleteTenantCredentialRequest
	_ = c.ShouldBindJSON(&req)

	param := &storageEntity.DeleteTenantCredential{
		CredentialID: credID,
		AccessKey:    req.AccessKey,
		BucketID:     bucketID,
		WorkspaceID:  workspaceID,
		TenantID:     tenantID,
		UserID:       userID,
		ZoneID:       zoneID,
	}

	err = h.tenantSvc.DeleteCredential(ctx, param)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			apires.RespondNotFound(c, "credential not found")
		} else {
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}
	apires.RespondSuccess(c, nil, "tenant credential deleted successfully")
}
