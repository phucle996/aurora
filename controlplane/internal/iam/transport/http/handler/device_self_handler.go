package iamHandler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	domainservice "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/pkg/apires"
	"controlplane/pkg/constant"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// [COMMENT]: DeviceSelfHandler quản lý thiết bị cá nhân của chính user đang hoạt động
type DeviceSelfHandler struct {
	deviceSvc domainservice.DeviceSelfService
}

// [COMMENT]: NewDeviceSelfHandler khởi tạo một thể hiện mới của DeviceSelfHandler
func NewDeviceSelfHandler(deviceSvc domainservice.DeviceSelfService) *DeviceSelfHandler {
	return &DeviceSelfHandler{deviceSvc: deviceSvc}
}

// [COMMENT]: ListMyDevices trả về danh sách thiết bị của chính user
func (h *DeviceSelfHandler) ListMyDevices(c *gin.Context) {
	const op = "iam.device.list_my_devices"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	userID, ok := constant.GetUserID(c, op)
	if !ok {
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	result, err := h.deviceSvc.ListMyDevices(ctx, userID, limit, offset)
	if err != nil {
		if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
			logger.HandlerWarn(c, op, err, "invalid argument")
			apires.RespondBadRequest(c, "invalid request")
			return
		}
		if errors.Is(err, iamTaxonomy.ErrInvalidSession) {
			logger.HandlerWarn(c, op, err, "unauthorized")
			apires.RespondUnauthorized(c, "unauthorized")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal_error")
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
	apires.RespondSuccess(c, gin.H{"items": presentationItems, "total": result.Total}, "ok")
}

// [COMMENT]: RevokeMyDevice thu hồi quyền truy cập của một thiết bị cụ thể thuộc sở hữu chính user
func (h *DeviceSelfHandler) RevokeMyDevice(c *gin.Context) {
	const op = "iam.device.revoke_my_device"
	ctx := constant.WithOperation(c.Request.Context(), op)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	userID, ok := constant.GetUserID(c, op)
	if !ok {
		return
	}

	did, err := uuid.Parse(c.Param("device_id"))
	if err != nil {
		apires.RespondBadRequest(c, "invalid device id")
		return
	}

	currentDeviceIDStr := constant.GetOptionalDeviceIDStr(c)
	var currentDeviceID uuid.UUID
	if currentDeviceIDStr != "" {
		if parsedID, err := uuid.Parse(currentDeviceIDStr); err == nil {
			currentDeviceID = parsedID
		}
	}

	err = h.deviceSvc.RevokeMyDevice(ctx, userID, did, currentDeviceID)
	if err != nil {
		if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			logger.HandlerWarn(c, op, err, "action not allowed - cannot revoke current device")
			apires.RespondForbidden(c, "cannot revoke current device")
			return
		}
		if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
			logger.HandlerWarn(c, op, err, "invalid argument")
			apires.RespondBadRequest(c, "invalid request")
			return
		}
		if errors.Is(err, iamTaxonomy.ErrInvalidSession) {
			logger.HandlerWarn(c, op, err, "forbidden revoke")
			apires.RespondForbidden(c, "forbidden")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal_error")
		return
	}
	c.Status(http.StatusNoContent)
}

// [COMMENT]: LogoutOtherDevices đăng xuất khỏi tất cả các thiết bị khác
func (h *DeviceSelfHandler) LogoutOtherDevices(c *gin.Context) {
	const op = "iam.device.logout_other_devices"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	userID, ok := constant.GetUserID(c, op)
	if !ok {
		return
	}

	currID, ok := constant.GetDeviceID(c, op)
	if !ok {
		return
	}
	affected, err := h.deviceSvc.LogoutOtherDevices(ctx, userID, &currID)
	if err != nil {
		if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
			logger.HandlerWarn(c, op, err, "invalid argument")
			apires.RespondBadRequest(c, "invalid request")
			return
		}
		if errors.Is(err, iamTaxonomy.ErrInvalidSession) {
			logger.HandlerWarn(c, op, err, "unauthorized")
			apires.RespondUnauthorized(c, "unauthorized")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal_error")
		return
	}
	apires.RespondSuccess(c, gin.H{"revoked_sessions": affected}, "ok")
}

// [COMMENT]: LogoutAllDevices đăng xuất hoàn toàn trên toàn bộ thiết bị
func (h *DeviceSelfHandler) LogoutAllDevices(c *gin.Context) {
	const op = "iam.device.logout_all_devices"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	userIDStr := strings.TrimSpace(c.GetHeader("x-user-id"))
	if userIDStr == "" {
		logger.HandlerWarn(c, op, nil, "unauthorized - missing x-user-id header")
		apires.RespondUnauthorized(c, "unauthorized")
		return
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		logger.HandlerWarn(c, op, err, "invalid user id format")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	affected, err := h.deviceSvc.LogoutAllDevices(ctx, userID)
	if err != nil {
		if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
			logger.HandlerWarn(c, op, err, "invalid argument")
			apires.RespondBadRequest(c, "invalid request")
			return
		}
		if errors.Is(err, iamTaxonomy.ErrInvalidSession) {
			logger.HandlerWarn(c, op, err, "unauthorized")
			apires.RespondUnauthorized(c, "unauthorized")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal_error")
		return
	}
	apires.RespondSuccess(c, gin.H{"revoked_sessions": affected}, "ok")
}
