package storageHandler

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageSvcInterface "controlplane/internal/storage/domain/service"
	storageTaxonomy "controlplane/internal/storage/taxonomy"
	storageDto "controlplane/internal/storage/transport/http/dto"
	apires "controlplane/pkg/apires"
	"controlplane/pkg/constant"
	"controlplane/pkg/logger"
)

// [COMMENT]: BucketHandler tiếp nhận và điều phối các HTTP REST request quản trị Bucket.
type BucketHandler struct {
	tenantSvc   storageSvcInterface.TenantBucketService
	personalSvc storageSvcInterface.PersonalBucketService
}

// [COMMENT]: NewBucketHandler khởi tạo controller xử lý các endpoint Bucket.
func NewBucketHandler(
	tenantSvc storageSvcInterface.TenantBucketService,
	personalSvc storageSvcInterface.PersonalBucketService,
) *BucketHandler {
	return &BucketHandler{
		tenantSvc:   tenantSvc,
		personalSvc: personalSvc,
	}
}

func (h *BucketHandler) Create(c *gin.Context) {
	const op = "storage.bucket.create"
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// 1. Trích xuất thông tin định danh từ HTTP Headers (đã được ACR & middleware validate)
	userIDStr := strings.TrimSpace(c.GetHeader(constant.HeaderXUserID))
	workspaceIDStr := strings.TrimSpace(c.GetHeader(constant.HeaderXWorkspaceID))
	zoneIDStr := strings.TrimSpace(c.GetHeader(constant.HeaderXZoneID))
	tenantIDStr := strings.TrimSpace(c.GetHeader(constant.HeaderXTenantID))

	if userIDStr == "" || workspaceIDStr == "" || zoneIDStr == "" {
		apires.RespondBadRequest(c, "missing mandatory identity or target workspace/zone headers")
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid user id format")
		return
	}

	workspaceID, err := uuid.Parse(workspaceIDStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid workspace id format")
		return
	}

	zoneID, err := uuid.Parse(zoneIDStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid zone id format")
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

	// 3. Rẽ nhánh xử lý gọi Service tương ứng tại Handler (Kiểm tra Personal trước để fast-path)
	var createErr error

	if tenantIDStr == "" {
		param := &storageEntity.CreatePersonalBucket{
			Name:               bucketName,
			WorkspaceID:        workspaceID,
			ZoneID:             zoneID,
			CapacityQuotaBytes: req.QuotaBytes,
			UserID:             userID,
		}
		createErr = h.personalSvc.CreateBucketForPersonal(ctx, param)
	} else {
		tenantID, err := uuid.Parse(tenantIDStr)
		if err != nil {
			apires.RespondBadRequest(c, "invalid tenant id format")
			return
		}
		param := &storageEntity.CreateTenantBucket{
			Name:               bucketName,
			WorkspaceID:        workspaceID,
			ZoneID:             zoneID,
			TenantID:           tenantID,
			CapacityQuotaBytes: req.QuotaBytes,
			UserID:             userID,
		}
		createErr = h.tenantSvc.CreateBucketForTenant(ctx, param)
	}

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

	// [COMMENT]: Không trả lại payload bucket chi tiết, chỉ trả về thành công 201 Created như yêu cầu của thiết kế
	apires.RespondCreated(c, nil, "bucket creation initiated")
}

func (h *BucketHandler) Get(c *gin.Context) {
	const op = "storage.bucket.get"
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	idStr := c.Param("id")
	bucketID, err := uuid.Parse(idStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	tenantIDStr := strings.TrimSpace(c.GetHeader(constant.HeaderXTenantID))
	var bucket interface{}
	var getErr error

	if tenantIDStr == "" {
		bucket, getErr = h.personalSvc.GetBucket(ctx, bucketID)
	} else {
		bucket, getErr = h.tenantSvc.GetBucket(ctx, bucketID)
	}

	if getErr != nil {
		if errors.Is(getErr, storageTaxonomy.ErrNotFound) {
			apires.RespondNotFound(c, "bucket not found")
			return
		}
		logger.HandlerError(c, op, getErr)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	apires.RespondSuccess(c, bucket, "get bucket details success")
}

func (h *BucketHandler) List(c *gin.Context) {
	const op = "storage.bucket.list"
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	tenantIDStr := strings.TrimSpace(c.GetHeader(constant.HeaderXTenantID))
	zoneIDStr := strings.TrimSpace(c.GetHeader(constant.HeaderXZoneID))
	workspaceIDStr := strings.TrimSpace(c.GetHeader(constant.HeaderXWorkspaceID))

	var list interface{}
	var listErr error

	if tenantIDStr == "" {
		if workspaceIDStr == "" {
			apires.RespondBadRequest(c, "missing workspace context in headers for listing personal buckets")
			return
		}
		workspaceID, err := uuid.Parse(workspaceIDStr)
		if err != nil {
			apires.RespondBadRequest(c, "invalid workspace id format")
			return
		}
		list, listErr = h.personalSvc.ListBuckets(ctx, workspaceID)
	} else {
		if zoneIDStr == "" {
			apires.RespondBadRequest(c, "missing zone context in headers for listing tenant buckets")
			return
		}
		tenantID, err := uuid.Parse(tenantIDStr)
		if err != nil {
			apires.RespondBadRequest(c, "invalid tenant id format")
			return
		}
		zoneID, err := uuid.Parse(zoneIDStr)
		if err != nil {
			apires.RespondBadRequest(c, "invalid zone id format")
			return
		}
		list, listErr = h.tenantSvc.ListBuckets(ctx, tenantID, zoneID)
	}

	if listErr != nil {
		logger.HandlerError(c, op, listErr)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	apires.RespondSuccess(c, list, "list buckets success")
}

func (h *BucketHandler) UpdateQuota(c *gin.Context) {
	const op = "storage.bucket.update_quota"
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	idStr := c.Param("id")
	bucketID, err := uuid.Parse(idStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	// 2. Bind JSON Request Body sử dụng cấu trúc DTO
	var req storageDto.UpdateQuotaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, "invalid body payload: "+err.Error())
		return
	}

	tenantIDStr := strings.TrimSpace(c.GetHeader(constant.HeaderXTenantID))
	var updateErr error

	if tenantIDStr == "" {
		updateErr = h.personalSvc.UpdateBucketQuota(ctx, bucketID, req.QuotaBytes)
	} else {
		updateErr = h.tenantSvc.UpdateBucketQuota(ctx, bucketID, req.QuotaBytes)
	}

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

func (h *BucketHandler) Suspend(c *gin.Context) {
	const op = "storage.bucket.suspend"
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	idStr := c.Param("id")
	bucketID, err := uuid.Parse(idStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	tenantIDStr := strings.TrimSpace(c.GetHeader(constant.HeaderXTenantID))
	var actionErr error

	if tenantIDStr == "" {
		actionErr = h.personalSvc.SuspendBucket(ctx, bucketID)
	} else {
		actionErr = h.tenantSvc.SuspendBucket(ctx, bucketID)
	}

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

func (h *BucketHandler) Resume(c *gin.Context) {
	const op = "storage.bucket.resume"
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	idStr := c.Param("id")
	bucketID, err := uuid.Parse(idStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	tenantIDStr := strings.TrimSpace(c.GetHeader(constant.HeaderXTenantID))
	var actionErr error

	if tenantIDStr == "" {
		actionErr = h.personalSvc.ResumeBucket(ctx, bucketID)
	} else {
		actionErr = h.tenantSvc.ResumeBucket(ctx, bucketID)
	}

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

func (h *BucketHandler) Delete(c *gin.Context) {
	const op = "storage.bucket.delete"
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	idStr := c.Param("id")
	bucketID, err := uuid.Parse(idStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	tenantIDStr := strings.TrimSpace(c.GetHeader(constant.HeaderXTenantID))
	var deleteErr error

	if tenantIDStr == "" {
		deleteErr = h.personalSvc.DeleteBucket(ctx, bucketID)
	} else {
		deleteErr = h.tenantSvc.DeleteBucket(ctx, bucketID)
	}

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
