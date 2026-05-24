package iamHandler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"controlplane/internal/http/middleware"
	domainservice "controlplane/internal/iam/domain/service"
	iamErrorx "controlplane/internal/iam/errorx"
	"controlplane/pkg/apires"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
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
	userID := middleware.GetUserID(c)
	if userID == "" {
		apires.RespondUnauthorized(c, "unauthorized")
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	result, err := h.deviceSvc.ListMyDevices(ctx, userID, limit, offset)
	if err != nil {
		if errors.Is(err, iamErrorx.ErrInvalidArgument) {
			logger.HandlerWarn(c, op, err, "invalid argument")
			apires.RespondBadRequest(c, "invalid request")
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
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	userID := middleware.GetUserID(c)
	if userID == "" {
		apires.RespondUnauthorized(c, "unauthorized")
		return
	}
	var ip *string
	if v := strings.TrimSpace(c.ClientIP()); v != "" {
		ip = &v
	}
	var userAgent *string
	if v := strings.TrimSpace(c.Request.UserAgent()); v != "" {
		userAgent = &v
	}
	deviceID := c.Param("device_id")
	err := h.deviceSvc.RevokeMyDevice(ctx, userID, deviceID, ip, userAgent)
	if err != nil {
		if errors.Is(err, iamErrorx.ErrInvalidArgument) {
			logger.HandlerWarn(c, op, err, "invalid argument")
			apires.RespondBadRequest(c, "invalid request")
			return
		}
		if errors.Is(err, iamErrorx.ErrInvalidSession) {
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
	userID := middleware.GetUserID(c)
	if userID == "" {
		apires.RespondUnauthorized(c, "unauthorized")
		return
	}
	var ip *string
	if v := strings.TrimSpace(c.ClientIP()); v != "" {
		ip = &v
	}
	var userAgent *string
	if v := strings.TrimSpace(c.Request.UserAgent()); v != "" {
		userAgent = &v
	}
	currentTrackedDeviceID := middleware.GetTrackedDeviceID(c)
	if currentTrackedDeviceID == "" {
		apires.RespondUnauthorized(c, "unauthorized")
		return
	}
	affected, err := h.deviceSvc.LogoutOtherDevices(ctx, userID, currentTrackedDeviceID, ip, userAgent)
	if err != nil {
		if errors.Is(err, iamErrorx.ErrInvalidArgument) {
			logger.HandlerWarn(c, op, err, "invalid argument")
			apires.RespondBadRequest(c, "invalid request")
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
	userID := middleware.GetUserID(c)
	if userID == "" {
		apires.RespondUnauthorized(c, "unauthorized")
		return
	}
	var ip *string
	if v := strings.TrimSpace(c.ClientIP()); v != "" {
		ip = &v
	}
	var userAgent *string
	if v := strings.TrimSpace(c.Request.UserAgent()); v != "" {
		userAgent = &v
	}
	affected, err := h.deviceSvc.LogoutAllDevices(ctx, userID, ip, userAgent)
	if err != nil {
		if errors.Is(err, iamErrorx.ErrInvalidArgument) {
			logger.HandlerWarn(c, op, err, "invalid argument")
			apires.RespondBadRequest(c, "invalid request")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal_error")
		return
	}
	apires.RespondSuccess(c, gin.H{"revoked_sessions": affected}, "ok")
}
