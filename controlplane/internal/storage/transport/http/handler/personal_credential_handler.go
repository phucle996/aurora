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

	workspaceID, ok := pkgcontext.GetWorkspaceID(c, op)
	if !ok {
		return
	}

	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}

	bucketName := strings.TrimSpace(c.Param("id"))
	if bucketName == "" {
		apires.RespondBadRequest(c, "missing mandatory bucket name")
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
		BucketName:  bucketName,
		Policy:      req.Policy,
		UserID:      userID,
		WorkspaceID: workspaceID,
		ZoneID:      zoneID,
	}
	cred, err := h.personalSvc.CreateCredential(ctx, param)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			apires.RespondNotFound(c, "bucket not found")
		} else if errors.Is(err, storageTaxonomy.ErrInvalidPolicy) {
			apires.RespondBadRequest(c, err.Error())
		} else {
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	// [COMMENT]: Viết inline phản hồi bằng gin.H thay vì sử dụng struct DTO
	res := gin.H{
		"id":         cred.ID.String(),
		"access_key": cred.AccessKey,
		"secret_key": cred.SecretKey, // Chứa raw secret key
		"policy":     cred.Policy,
		"created_at": cred.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at": cred.UpdatedAt.UTC().Format(time.RFC3339),
	}
	apires.RespondCreated(c, res, "credential created successfully")
}

// [COMMENT]: Get lấy thông tin chi tiết một credential cá nhân.
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

	// [COMMENT]: Trả về danh sách dạng inline bằng slice of gin.H thay vì DTO slice
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
	apires.RespondSuccess(c, res, "success")
}

// [COMMENT]: Delete xóa bỏ access credential cá nhân thuộc một bucket xác định.
func (h *PersonalCredentialHandler) Delete(c *gin.Context) {
	const op = "storage.personal_credential.delete"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Lấy workspace_id từ context để thực hiện validate chéo scope
	workspaceID, ok := pkgcontext.GetWorkspaceID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: Lấy zone_id từ context (middleware-injected từ workspace → zone mapping).
	// Zone là thuộc tính cố định của workspace và được ghi trực tiếp vào outbox.
	zoneID, ok := pkgcontext.GetZoneID(c, op)
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

	credIDStr := strings.TrimSpace(c.Param("credential_id"))
	if credIDStr == "" {
		apires.RespondBadRequest(c, "missing credential id")
		return
	}

	credID, err := uuid.Parse(credIDStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid credential id format")
		return
	}

	// [COMMENT]: Bind request body để lấy access_key — FE đã có access_key từ List response
	var req storageDto.DeleteCredentialRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, "missing or invalid access_key in request body")
		return
	}

	// [COMMENT]: Tạo thực thể chứa đầy đủ tham số để validate chéo scope đa thuê
	param := &storageEntity.DeletePersonalCredential{
		CredentialID: credID,
		AccessKey:    req.AccessKey,
		BucketID:     bucketID,
		WorkspaceID:  workspaceID,
		UserID:       userID,
		ZoneID:       zoneID,
	}

	// [COMMENT]: Gọi service xóa credential với xác thực chéo các ID
	err = h.personalSvc.DeleteCredential(ctx, param)
	if err != nil {
		if errors.Is(err, storageTaxonomy.ErrNotFound) {
			apires.RespondNotFound(c, "credential not found")
		} else {
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}
	apires.RespondSuccess(c, nil, "credential deleted successfully")
}
