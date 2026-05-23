package coreHandler

import (
	"context"
	"errors"
	"strings"
	"time"

	coreEntity "controlplane/internal/core/domain/entity"
	domainservice "controlplane/internal/core/domain/service"
	coreErrorx "controlplane/internal/core/errorx"
	requestdto "controlplane/internal/core/transport/http/dto/req"
	apires "controlplane/pkg/apires"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
)

type ZoneHandler struct {
	zoneSvc domainservice.ZoneService
}

func NewZoneHandler(zoneSvc domainservice.ZoneService) *ZoneHandler {
	return &ZoneHandler{zoneSvc: zoneSvc}
}

func (h *ZoneHandler) CreateZone(c *gin.Context) {
	const op = "core.zone.create"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	var request requestdto.CreateZoneRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.HandlerWarn(c, op, err, "bind create zone request failed")
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	var status *coreEntity.ZoneStatus
	if trimmed := strings.TrimSpace(request.Status); trimmed != "" {
		parsed := coreEntity.ZoneStatus(strings.ToLower(trimmed))
		status = &parsed
	}
	zone, err := h.zoneSvc.CreateZone(ctx, request.Code, request.Name, status)
	if err != nil {
		switch {
		case errors.Is(err, coreErrorx.ErrZoneInvalidInput):
			logger.HandlerWarn(c, op, err, "create zone invalid input")
			apires.RespondBadRequest(c, "invalid request")
		case errors.Is(err, coreErrorx.ErrZoneCodeAlreadyExists):
			logger.HandlerWarn(c, op, err, "create zone conflict")
			apires.RespondConflict(c, "resource already exists")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}
	apires.RespondCreated(c, zone, "zone created")
}

func (h *ZoneHandler) ListZones(c *gin.Context) {
	const op = "core.zone.list"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	items, err := h.zoneSvc.ListZones(ctx)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal_error")
		return
	}
	apires.RespondSuccess(c, gin.H{"items": items, "total": len(items)}, "zones fetched")
}

func (h *ZoneHandler) UpdateZoneStatus(c *gin.Context) {
	const op = "core.zone.update_status"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	zoneID := strings.TrimSpace(c.Param("zone_id"))
	var request requestdto.UpdateZoneStatusRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.HandlerWarn(c, op, err, "bind update zone status request failed")
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	status := coreEntity.ZoneStatus(strings.ToLower(strings.TrimSpace(request.Status)))
	zone, err := h.zoneSvc.UpdateZoneStatus(ctx, zoneID, status)
	if err != nil {
		switch {
		case errors.Is(err, coreErrorx.ErrZoneInvalidInput):
			logger.HandlerWarn(c, op, err, "update zone invalid input")
			apires.RespondBadRequest(c, "invalid request")
		case errors.Is(err, coreErrorx.ErrZoneNotFound):
			logger.HandlerWarn(c, op, err, "zone not found")
			apires.RespondNotFound(c, "resource not found")
		case errors.Is(err, coreErrorx.ErrZoneInvalidTransition):
			logger.HandlerWarn(c, op, err, "zone invalid transition")
			apires.RespondConflict(c, "state conflict")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}
	apires.RespondSuccess(c, zone, "zone status updated")
}

func (h *ZoneHandler) DeleteZone(c *gin.Context) {
	const op = "core.zone.delete"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	zoneID := strings.TrimSpace(c.Param("zone_id"))
	if err := h.zoneSvc.DeleteZone(ctx, zoneID); err != nil {
		switch {
		case errors.Is(err, coreErrorx.ErrZoneInvalidInput):
			logger.HandlerWarn(c, op, err, "delete zone invalid input")
			apires.RespondBadRequest(c, "invalid request")
		case errors.Is(err, coreErrorx.ErrZoneNotFound):
			logger.HandlerWarn(c, op, err, "zone not found")
			apires.RespondNotFound(c, "resource not found")
		case errors.Is(err, coreErrorx.ErrZoneDeletePreconditionFailed):
			logger.HandlerWarn(c, op, err, "delete precondition failed")
			apires.RespondConflict(c, "zone delete precondition failed")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}
	apires.RespondSuccess(c, nil, "zone deleted")
}

func (h *ZoneHandler) ListZoneServices(c *gin.Context) {
	const op = "core.zone_service.list"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	zoneID := strings.TrimSpace(c.Param("zone_id"))
	items, err := h.zoneSvc.ListZoneServices(ctx, zoneID)
	if err != nil {
		switch {
		case errors.Is(err, coreErrorx.ErrZoneServiceInvalidInput):
			apires.RespondBadRequest(c, "invalid request")
		case errors.Is(err, coreErrorx.ErrZoneServiceZoneNotFound):
			apires.RespondNotFound(c, "resource not found")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}
	apires.RespondSuccess(c, items, "zone services fetched")
}

func (h *ZoneHandler) UpsertZoneService(c *gin.Context) {
	const op = "core.zone_service.upsert"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	zoneID := strings.TrimSpace(c.Param("zone_id"))
	var request requestdto.UpsertZoneServiceRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	serviceType := strings.ToLower(strings.TrimSpace(request.ServiceType))
	switch coreEntity.ZoneServiceType(serviceType) {
	case coreEntity.ZoneServiceTypeMail, coreEntity.ZoneServiceTypeHypervisor, coreEntity.ZoneServiceTypeK8s, coreEntity.ZoneServiceTypeAI:
	default:
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	item, err := h.zoneSvc.UpsertZoneService(ctx, zoneID, serviceType, request.Enabled)
	if err != nil {
		switch {
		case errors.Is(err, coreErrorx.ErrZoneServiceInvalidInput), errors.Is(err, coreErrorx.ErrZoneServiceInvalidType):
			apires.RespondBadRequest(c, "invalid request")
		case errors.Is(err, coreErrorx.ErrZoneServiceZoneNotFound):
			apires.RespondNotFound(c, "resource not found")
		case errors.Is(err, coreErrorx.ErrZoneServiceStateConflict):
			apires.RespondConflict(c, "state conflict")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}
	apires.RespondSuccess(c, item, "zone service updated")
}
