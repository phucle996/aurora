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
	"controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// [COMMENT]: CredentialHandler quản lý các request HTTP tương tác với Access Keys của MinIO.
type CredentialHandler struct {
	tenantSvc   storageSvcInterface.TenantCredentialService
	personalSvc storageSvcInterface.PersonalCredentialService
}

// [COMMENT]: NewCredentialHandler khởi tạo controller quản lý key credentials.
func NewCredentialHandler(
	tenantSvc storageSvcInterface.TenantCredentialService,
	personalSvc storageSvcInterface.PersonalCredentialService,
) *CredentialHandler {
	return &CredentialHandler{
		tenantSvc:   tenantSvc,
		personalSvc: personalSvc,
	}
}

func (h *CredentialHandler) Create(c *gin.Context) {
	const op = "storage.credential.create"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// 1. Trích xuất danh tính và path params
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	tenantIDStr := pkgcontext.GetOptionalTenantIDStr(c)
	bucketIDStr := strings.TrimSpace(c.Param("id"))

	if bucketIDStr == "" {
		apires.RespondBadRequest(c, "missing mandatory bucket id")
		return
	}

	bucketID, err := uuid.Parse(bucketIDStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	// 2. Bind Request Body
	var req storageDto.CreateCredentialRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, "invalid request payload")
		return
	}

	// 3. Rẽ nhánh xử lý gọi Service
	if tenantIDStr == "" {
		param := &storageEntity.CreatePersonalCredential{
			BucketID: bucketID,
			Policy:   req.Policy,
			UserID:   userID,
		}
		cred, err := h.personalSvc.CreateCredential(ctx, param)
		if err != nil {
			if errors.Is(err, storageTaxonomy.ErrNotFound) {
				apires.RespondNotFound(c, "bucket not found")
			} else {
				logger.HandlerError(c, op, err)
				apires.RespondInternalError(c, "internal_error")
			}
			return
		}

		res := &storageDto.CredentialResponse{
			ID:        cred.ID.String(),
			BucketID:  cred.BucketID.String(),
			AccessKey: cred.AccessKey,
			SecretKey: cred.SecretKey, // Chứa raw secret key
			Policy:    cred.Policy,
			CreatedAt: cred.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt: cred.UpdatedAt.UTC().Format(time.RFC3339),
		}
		apires.RespondCreated(c, res, "credential created successfully")
	} else {
		param := &storageEntity.CreateTenantCredential{
			BucketID: bucketID,
			Policy:   req.Policy,
			UserID:   userID,
		}
		cred, err := h.tenantSvc.CreateCredential(ctx, param)
		if err != nil {
			if errors.Is(err, storageTaxonomy.ErrNotFound) {
				apires.RespondNotFound(c, "bucket not found")
			} else {
				logger.HandlerError(c, op, err)
				apires.RespondInternalError(c, "internal_error")
			}
			return
		}

		res := &storageDto.CredentialResponse{
			ID:        cred.ID.String(),
			BucketID:  cred.BucketID.String(),
			AccessKey: cred.AccessKey,
			SecretKey: cred.SecretKey, // Chứa raw secret key
			Policy:    cred.Policy,
			CreatedAt: cred.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt: cred.UpdatedAt.UTC().Format(time.RFC3339),
		}
		apires.RespondCreated(c, res, "credential created successfully")
	}
}

func (h *CredentialHandler) Get(c *gin.Context) {
	const op = "storage.credential.get"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	tenantIDStr := pkgcontext.GetOptionalTenantIDStr(c)
	credIDStr := strings.TrimSpace(c.Param("id"))

	if credIDStr == "" {
		apires.RespondBadRequest(c, "missing credential id")
		return
	}

	credID, err := uuid.Parse(credIDStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid credential id format")
		return
	}

	if tenantIDStr == "" {
		cred, err := h.personalSvc.GetCredential(ctx, credID)
		if err != nil {
			if errors.Is(err, storageTaxonomy.ErrNotFound) {
				apires.RespondNotFound(c, "credential not found")
			} else {
				logger.HandlerError(c, op, err)
				apires.RespondInternalError(c, "internal_error")
			}
			return
		}
		if cred == nil {
			apires.RespondNotFound(c, "credential not found")
			return
		}

		res := &storageDto.CredentialResponse{
			ID:        cred.ID.String(),
			BucketID:  cred.BucketID.String(),
			AccessKey: cred.AccessKey,
			Policy:    cred.Policy,
			CreatedAt: cred.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt: cred.UpdatedAt.UTC().Format(time.RFC3339),
		}
		apires.RespondSuccess(c, res, "success")
	} else {
		cred, err := h.tenantSvc.GetCredential(ctx, credID)
		if err != nil {
			if errors.Is(err, storageTaxonomy.ErrNotFound) {
				apires.RespondNotFound(c, "credential not found")
			} else {
				logger.HandlerError(c, op, err)
				apires.RespondInternalError(c, "internal_error")
			}
			return
		}
		if cred == nil {
			apires.RespondNotFound(c, "credential not found")
			return
		}

		res := &storageDto.CredentialResponse{
			ID:        cred.ID.String(),
			BucketID:  cred.BucketID.String(),
			AccessKey: cred.AccessKey,
			Policy:    cred.Policy,
			CreatedAt: cred.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt: cred.UpdatedAt.UTC().Format(time.RFC3339),
		}
		apires.RespondSuccess(c, res, "success")
	}
}

func (h *CredentialHandler) List(c *gin.Context) {
	const op = "storage.credential.list"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	tenantIDStr := pkgcontext.GetOptionalTenantIDStr(c)
	bucketIDStr := strings.TrimSpace(c.Param("id"))

	if bucketIDStr == "" {
		apires.RespondBadRequest(c, "missing bucket id")
		return
	}

	bucketID, err := uuid.Parse(bucketIDStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	if tenantIDStr == "" {
		creds, err := h.personalSvc.ListCredentials(ctx, bucketID)
		if err != nil {
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
			return
		}

		var res []*storageDto.CredentialResponse
		for _, cred := range creds {
			res = append(res, &storageDto.CredentialResponse{
				ID:        cred.ID.String(),
				BucketID:  cred.BucketID.String(),
				AccessKey: cred.AccessKey,
				Policy:    cred.Policy,
				CreatedAt: cred.CreatedAt.UTC().Format(time.RFC3339),
				UpdatedAt: cred.UpdatedAt.UTC().Format(time.RFC3339),
			})
		}
		apires.RespondSuccess(c, res, "success")
	} else {
		creds, err := h.tenantSvc.ListCredentials(ctx, bucketID)
		if err != nil {
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
			return
		}

		var res []*storageDto.CredentialResponse
		for _, cred := range creds {
			res = append(res, &storageDto.CredentialResponse{
				ID:        cred.ID.String(),
				BucketID:  cred.BucketID.String(),
				AccessKey: cred.AccessKey,
				Policy:    cred.Policy,
				CreatedAt: cred.CreatedAt.UTC().Format(time.RFC3339),
				UpdatedAt: cred.UpdatedAt.UTC().Format(time.RFC3339),
			})
		}
		apires.RespondSuccess(c, res, "success")
	}
}

func (h *CredentialHandler) Revoke(c *gin.Context) {
	const op = "storage.credential.revoke"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	tenantIDStr := pkgcontext.GetOptionalTenantIDStr(c)
	credIDStr := strings.TrimSpace(c.Param("id"))

	if credIDStr == "" {
		apires.RespondBadRequest(c, "missing credential id")
		return
	}

	credID, err := uuid.Parse(credIDStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid credential id format")
		return
	}

	if tenantIDStr == "" {
		err := h.personalSvc.RevokeCredential(ctx, credID, userID)
		if err != nil {
			if errors.Is(err, storageTaxonomy.ErrNotFound) {
				apires.RespondNotFound(c, "credential not found")
			} else {
				logger.HandlerError(c, op, err)
				apires.RespondInternalError(c, "internal_error")
			}
			return
		}
		apires.RespondSuccess(c, nil, "credential revoked successfully")
	} else {
		err := h.tenantSvc.RevokeCredential(ctx, credID, userID)
		if err != nil {
			if errors.Is(err, storageTaxonomy.ErrNotFound) {
				apires.RespondNotFound(c, "credential not found")
			} else {
				logger.HandlerError(c, op, err)
				apires.RespondInternalError(c, "internal_error")
			}
			return
		}
		apires.RespondSuccess(c, nil, "credential revoked successfully")
	}
}
