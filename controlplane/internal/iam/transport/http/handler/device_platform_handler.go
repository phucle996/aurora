package iamHandler

import (
	"context"
	"errors"
	"time"

	domainservice "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/pkg/apires"
	"controlplane/pkg/constant"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// [COMMENT]: DevicePlatformHandler quản lý và giám sát thiết bị toàn platform dành cho Platform Admin
type DevicePlatformHandler struct {
	deviceSvc domainservice.DevicePlatformService
}

// [COMMENT]: NewDevicePlatformHandler khởi tạo một thể hiện mới của DevicePlatformHandler
func NewDevicePlatformHandler(deviceSvc domainservice.DevicePlatformService) *DevicePlatformHandler {
	return &DevicePlatformHandler{deviceSvc: deviceSvc}
}

// [COMMENT]: ListUserDevicesPlatform lấy danh sách thiết bị của một user bất kỳ (phải có level cao hơn target user)
func (h *DevicePlatformHandler) ListUserDevicesPlatform(c *gin.Context) {
	const op = "iam.device.list_user_devices_platform"
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// 1. Lấy target user ID từ router param
	targetUserIDStr := c.Param("id")
	targetUserID, err := uuid.Parse(targetUserIDStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid target user id format")
		return
	}

	// 2. Lấy caller level từ header
	callerLevel, ok := constant.GetUserLevel(c, op)
	if !ok {
		return
	}

	// [COMMENT]: 3. Gọi service truy vấn danh sách thiết bị (kiểm tra phân cấp được thực hiện trong 1 RTT ở Repo)
	result, err := h.deviceSvc.ListUserDevicesPlatform(ctx, targetUserID, int32(callerLevel), 100, 0)
	if err != nil {
		if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			logger.HandlerWarn(c, op, err, "insufficient level hierarchy to audit devices")
			apires.RespondForbidden(c, "insufficient_level_hierarchy")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal error occurred")
		return
	}

	presentationItems := make([]gin.H, 0, len(result.Devices))
	for _, item := range result.Devices {
		// [COMMENT]: Phái sinh trạng thái động từ cột mốc revoked_at và runtime IsOnline
		status := "active"
		if item.RevokedAt != nil {
			status = "revoked"
		} else if item.IsOnline {
			status = "online"
		}

		// [COMMENT]: Đóng gói thông tin thiết bị dưới dạng flat + nested object tương thích ngược với Cloud Console
		presentationItems = append(presentationItems, gin.H{
			"device": gin.H{
				"id":          item.ID,
				"device_name": item.DeviceName,
				"status":      status,
			},
			"is_online":            item.IsOnline,
			"last_seen_at":         item.LastSeenAt,
			"last_seen_ip":         item.LastIP,
			"last_seen_user_agent": item.LastUA,
		})
	}
	apires.RespondSuccess(c, gin.H{"items": presentationItems, "total": result.Total}, "success")
}
