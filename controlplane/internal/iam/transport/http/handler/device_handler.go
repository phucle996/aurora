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

type DeviceHandler struct {
	deviceSvc domainservice.DeviceService
}

func NewDeviceHandler(deviceSvc domainservice.DeviceService) *DeviceHandler {
	return &DeviceHandler{deviceSvc: deviceSvc}
}

func (h *DeviceHandler) ListMyDevices(c *gin.Context) {
	const op = "iam.device.list_my_devices"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	// [COMMENT]: Lấy userID trực tiếp từ x-user-id header do API Gateway chuyển tiếp xuống.
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

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	// [COMMENT]: Truyền userID trực tiếp vào service method.
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
		presentationItems = append(presentationItems, gin.H{
			"device":               item.Device,
			"is_online":            item.IsOnline,
			"last_seen_at":         item.LastSeenAt,
			"last_seen_ip":         item.LastIP,
			"last_seen_user_agent": item.LastUA,
		})
	}
	apires.RespondSuccess(c, gin.H{"items": presentationItems, "total": result.Total}, "ok")
}

func (h *DeviceHandler) RevokeMyDevice(c *gin.Context) {
	const op = "iam.device.revoke_my_device"
	// Khởi tạo context với timeout và tiêm tên operation vào context
	ctx := constant.WithOperation(c.Request.Context(), op)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// [COMMENT]: Lấy userID từ HTTP header được điền bởi biên bảo mật (Edge/Gateway).
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

	did, err := uuid.Parse(c.Param("device_id"))
	if err != nil {
		apires.RespondBadRequest(c, "invalid device id")
		return
	}

	// [COMMENT]: Đọc header client device id (x-device-id) do Envoy forward xuống.
	currentDeviceIDStr := strings.TrimSpace(c.GetHeader("x-device-id"))
	var currentDeviceID uuid.UUID
	if currentDeviceIDStr != "" {
		if parsedID, err := uuid.Parse(currentDeviceIDStr); err == nil {
			currentDeviceID = parsedID
		}
	}

	// [COMMENT]: Truyền userID và currentDeviceID trực tiếp vào service method.
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

func (h *DeviceHandler) LogoutOtherDevices(c *gin.Context) {
	const op = "iam.device.logout_other_devices"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	// [COMMENT]: Lấy userID trực tiếp từ Gateway header.
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

	currentTrackedDeviceID := strings.TrimSpace(c.GetHeader(constant.HeaderXDeviceID))
	if currentTrackedDeviceID == "" {
		apires.RespondUnauthorized(c, "unauthorized")
		return
	}
	currID, err := uuid.Parse(currentTrackedDeviceID)
	if err != nil {
		apires.RespondUnauthorized(c, "unauthorized")
		return
	}
	// [COMMENT]: Truyền userID nhận trực tiếp vào service.
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

func (h *DeviceHandler) LogoutAllDevices(c *gin.Context) {
	const op = "iam.device.logout_all_devices"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	// [COMMENT]: Lấy userID từ Gateway header.
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

	// [COMMENT]: Truyền userID trực tiếp vào service.
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
