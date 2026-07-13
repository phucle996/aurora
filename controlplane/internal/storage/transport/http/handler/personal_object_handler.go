package storageHandler

import (
	"context"
	"errors"
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

// [COMMENT]: PersonalObjectHandler xử lý các HTTP request thao tác đối tượng lưu trữ cá nhân.
type PersonalObjectHandler struct {
	personalSvc storageSvcInterface.PersonalObjectService
}

// [COMMENT]: NewPersonalObjectHandler khởi tạo controller xử lý các endpoint Objects cá nhân.
func NewPersonalObjectHandler(
	personalSvc storageSvcInterface.PersonalObjectService,
) *PersonalObjectHandler {
	return &PersonalObjectHandler{
		personalSvc: personalSvc,
	}
}

// [COMMENT]: RegisterObjectPresign đăng ký job xin cấp Presigned URL hoặc duyệt danh sách đối tượng (list/upload/download/delete).
func (h *PersonalObjectHandler) RegisterObjectPresign(c *gin.Context) {
	const op = "storage.personal_object.register_presign"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: 1. Trích xuất thông tin định danh
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	bucketIDStr := c.Param("id")
	bucketID, err := uuid.Parse(bucketIDStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid bucket id format")
		return
	}

	// [COMMENT]: 2. Bind Body JSON DTO
	var req storageDto.RequestObjectPresignRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, "invalid body payload: "+err.Error())
		return
	}

	// [COMMENT]: 3. Validate logic theo từng hành động
	action := storageEntity.SignObjectAction(req.Action)
	if action != storageEntity.SignObjectActionList && req.Key == "" {
		apires.RespondBadRequest(c, "key is required for action upload, download, and delete")
		return
	}

	// [COMMENT]: 4. Trích xuất workspaceID và zoneID
	workspaceID, ok := pkgcontext.GetWorkspaceID(c, op)
	if !ok {
		return
	}

	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}

	// [COMMENT]: 5. Chuyển đổi DTO sang tham số domain service
	param := &storageEntity.RequestObjectPresignParam{
		BucketID:    bucketID,
		BucketName:  req.BucketName,
		Key:         req.Key,
		Action:      action,
		ContentType: req.ContentType,
		UserID:      userID,
		WorkspaceID: workspaceID,
		ZoneID:      zoneID,
	}

	// [COMMENT]: 6. Gọi service đăng ký job
	eventID, err := h.personalSvc.RegisterObjectPresign(ctx, param)
	if err != nil {
		switch {
		case errors.Is(err, storageTaxonomy.ErrNotFound):
			logger.HandlerWarn(c, op, err, "bucket not found")
			apires.RespondNotFound(c, "bucket not found")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	// [COMMENT]: 7. Trả về mã 202 Accepted kèm event_id để client theo dõi kết quả qua WebSocket
	apires.RespondAccepted(c, gin.H{
		"event_id": eventID.String(),
	}, "object presign accepted")
}
