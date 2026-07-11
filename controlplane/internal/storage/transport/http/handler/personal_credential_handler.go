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

// [COMMENT]: PersonalCredentialHandler quản lý các request HTTP tương tác với Access Keys của MinIO cho cá nhân.
type PersonalCredentialHandler struct {
	personalSvc storageSvcInterface.PersonalCredentialService
}

// [COMMENT]: NewPersonalCredentialHandler khởi tạo controller quản lý key credentials cá nhân.
func NewPersonalCredentialHandler(
	personalSvc storageSvcInterface.PersonalCredentialService,
) *PersonalCredentialHandler {
	return &PersonalCredentialHandler{
		personalSvc: personalSvc,
	}
}

// [COMMENT]: Create tạo mới Access Key trong bucket cá nhân.
func (h *PersonalCredentialHandler) Create(c *gin.Context) {
	const op = "storage.personal_credential.create"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// 1. Trích xuất danh tính và path params trực tiếp
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

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

	// 3. Thực thi nghiệp vụ qua personal service
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
}

// [COMMENT]: Get lấy thông tin chi tiết một credential cá nhân.
func (h *PersonalCredentialHandler) Get(c *gin.Context) {
	const op = "storage.personal_credential.get"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// Trích xuất userID để xác thực quyền sở hữu
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

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

	cred, err := h.personalSvc.GetCredential(ctx, credID, userID)
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

// [COMMENT]: List trả về danh sách các access credentials của bucket cá nhân.
func (h *PersonalCredentialHandler) List(c *gin.Context) {
	const op = "storage.personal_credential.list"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// Trích xuất userID để xác thực quyền sở hữu
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

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

	creds, err := h.personalSvc.ListCredentials(ctx, bucketID, userID)
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

// [COMMENT]: Revoke thu hồi và vô hiệu hóa access credential cá nhân.
func (h *PersonalCredentialHandler) Revoke(c *gin.Context) {
	const op = "storage.personal_credential.revoke"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

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

	err = h.personalSvc.RevokeCredential(ctx, credID, userID)
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
