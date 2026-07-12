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

// [COMMENT]: PersonalBucketHandler xử lý các HTTP request quản trị Bucket của người dùng cá nhân/workspace.
type PersonalBucketHandler struct {
	personalSvc storageSvcInterface.PersonalBucketService
}

// [COMMENT]: NewPersonalBucketHandler khởi tạo controller xử lý các endpoint Bucket cá nhân.
func NewPersonalBucketHandler(
	personalSvc storageSvcInterface.PersonalBucketService,
) *PersonalBucketHandler {
	return &PersonalBucketHandler{
		personalSvc: personalSvc,
	}
}

// [COMMENT]: Create tiếp nhận yêu cầu tạo mới bucket cá nhân trong 1 zone.
func (h *PersonalBucketHandler) Create(c *gin.Context) {
	const op = "storage.personal_bucket.create"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// 1. Trích xuất thông tin định danh trực tiếp từ context
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

	// 2. Bind JSON Request Body sử dụng cấu trúc DTO
	var req storageDto.CreateBucketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, "invalid body payload: "+err.Error())
		return
	}

	bucketName := strings.TrimSpace(req.Name)
	if bucketName == "" {
		apires.RespondBadRequest(c, "bucket name cannot be empty")
		return
	}

	// [COMMENT]: Thực thi nghiệp vụ qua service cá nhân
	param := &storageEntity.CreatePersonalBucket{
		Name:               bucketName,
		WorkspaceID:        workspaceID,
		ZoneID:             zoneID,
		CapacityQuotaBytes: req.QuotaBytes,
		UserID:             userID,
	}
	createResult, createErr := h.personalSvc.CreateBucketForPersonal(ctx, param)

	if createErr != nil {
		switch {
		case errors.Is(createErr, storageTaxonomy.ErrAlreadyExists):
			logger.HandlerWarn(c, op, createErr, "bucket name conflict: "+bucketName)
			apires.RespondConflict(c, "bucket name already exists")
		case errors.Is(createErr, storageTaxonomy.ErrInvalidBucketName):
			logger.HandlerWarn(c, op, createErr, "invalid bucket name format")
			apires.RespondBadRequest(c, "invalid bucket name format")
		default:
			logger.HandlerError(c, op, createErr)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	apires.RespondCreated(c, gin.H{
		"bucket_id":     createResult.BucketID.String(),
		"bucket_name":   createResult.BucketName,
		"credential_id": createResult.CredentialID.String(),
		"access_key":    createResult.AccessKey,
		"secret_key":    createResult.SecretKey,
		"policy":        createResult.Policy,
	}, "bucket creation initiated — save your credentials, they will not be shown again")
}

// [COMMENT]: Get truy vấn thông tin chi tiết một bucket cá nhân.
func (h *PersonalBucketHandler) Get(c *gin.Context) {
	const op = "storage.personal_bucket.get"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// Trích xuất userID để xác thực quyền sở hữu
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	idStr := c.Param("id")
	bucketID, err := uuid.Parse(idStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	bucket, getErr := h.personalSvc.GetBucket(ctx, bucketID, userID)
	if getErr != nil {
		if errors.Is(getErr, storageTaxonomy.ErrNotFound) {
			apires.RespondNotFound(c, "bucket not found")
			return
		}
		logger.HandlerError(c, op, getErr)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	apires.RespondSuccess(c, gin.H{
		"id":                   bucket.ID.String(),
		"name":                 bucket.Name,
		"workspace_id":         bucket.WorkspaceID.String(),
		"zone_id":              bucket.ZoneID.String(),
		"status":               string(bucket.Status),
		"capacity_quota_bytes": bucket.CapacityQuotaBytes,
		"used_bytes":           bucket.UsedBytes,
		"created_at":           bucket.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":           bucket.UpdatedAt.UTC().Format(time.RFC3339),
	}, "get bucket details success")
}

// [COMMENT]: List trả về danh sách các bucket thuộc workspace cá nhân hiện tại.
func (h *PersonalBucketHandler) List(c *gin.Context) {
	const op = "storage.personal_bucket.list"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// Trích xuất userID để xác thực quyền sở hữu
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

	list, listErr := h.personalSvc.ListBuckets(ctx, workspaceID, zoneID, userID)
	if listErr != nil {
		logger.HandlerError(c, op, listErr)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	resList := make([]gin.H, len(list))
	for i, b := range list {
		resList[i] = gin.H{
			"id":                   b.ID.String(),
			"name":                 b.Name,
			"status":               string(b.Status),
			"capacity_quota_bytes": b.CapacityQuotaBytes,
			"used_bytes":           b.UsedBytes,
			"created_at":           b.CreatedAt.UTC().Format(time.RFC3339),
			"updated_at":           b.UpdatedAt.UTC().Format(time.RFC3339),
		}
	}

	apires.RespondSuccess(c, resList, "list buckets success")
}

// [COMMENT]: ListNames chỉ trả về mảng danh sách tên vật lý của các bucket cá nhân (truy vấn nhẹ).
func (h *PersonalBucketHandler) ListNames(c *gin.Context) {
	const op = "storage.personal_bucket.list_names"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// Trích xuất userID để xác thực quyền sở hữu
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

	names, listErr := h.personalSvc.ListBucketNames(ctx, workspaceID, zoneID, userID)
	if listErr != nil {
		logger.HandlerError(c, op, listErr)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	apires.RespondSuccess(c, names, "list bucket names success")
}

// [COMMENT]: UpdateQuota điều chỉnh dung lượng tối đa của bucket cá nhân.
func (h *PersonalBucketHandler) UpdateQuota(c *gin.Context) {
	const op = "storage.personal_bucket.update_quota"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// Trích xuất userID để xác thực quyền sở hữu
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	idStr := c.Param("id")
	bucketID, err := uuid.Parse(idStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	var req storageDto.UpdateQuotaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, "invalid body payload: "+err.Error())
		return
	}

	updateErr := h.personalSvc.UpdateBucketQuota(ctx, bucketID, userID, req.QuotaBytes)
	if updateErr != nil {
		if errors.Is(updateErr, storageTaxonomy.ErrNotFound) {
			apires.RespondNotFound(c, "bucket not found")
			return
		}
		logger.HandlerError(c, op, updateErr)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	apires.RespondSuccess(c, nil, "bucket quota updated")
}

// [COMMENT]: Suspend tạm dừng hoạt động bucket cá nhân.
func (h *PersonalBucketHandler) Suspend(c *gin.Context) {
	const op = "storage.personal_bucket.suspend"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// Trích xuất userID để xác thực quyền sở hữu
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	idStr := c.Param("id")
	bucketID, err := uuid.Parse(idStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	actionErr := h.personalSvc.SuspendBucket(ctx, bucketID, userID)
	if actionErr != nil {
		if errors.Is(actionErr, storageTaxonomy.ErrNotFound) {
			apires.RespondNotFound(c, "bucket not found")
			return
		}
		logger.HandlerError(c, op, actionErr)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	apires.RespondSuccess(c, nil, "bucket suspended")
}

// [COMMENT]: Resume tái kích hoạt bucket cá nhân đang suspend.
func (h *PersonalBucketHandler) Resume(c *gin.Context) {
	const op = "storage.personal_bucket.resume"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// Trích xuất userID để xác thực quyền sở hữu
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	idStr := c.Param("id")
	bucketID, err := uuid.Parse(idStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	actionErr := h.personalSvc.ResumeBucket(ctx, bucketID, userID)
	if actionErr != nil {
		if errors.Is(actionErr, storageTaxonomy.ErrNotFound) {
			apires.RespondNotFound(c, "bucket not found")
			return
		}
		logger.HandlerError(c, op, actionErr)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	apires.RespondSuccess(c, nil, "bucket resumed")
}

// [COMMENT]: Delete khởi động tiến trình xóa hoàn toàn bucket cá nhân.
func (h *PersonalBucketHandler) Delete(c *gin.Context) {
	const op = "storage.personal_bucket.delete"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// Trích xuất userID để xác thực quyền sở hữu
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	idStr := c.Param("id")
	bucketID, err := uuid.Parse(idStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	deleteErr := h.personalSvc.DeleteBucket(ctx, bucketID, userID)
	if deleteErr != nil {
		if errors.Is(deleteErr, storageTaxonomy.ErrNotFound) {
			apires.RespondNotFound(c, "bucket not found")
			return
		}
		logger.HandlerError(c, op, deleteErr)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	apires.RespondSuccess(c, nil, "bucket deletion initiated")
}
