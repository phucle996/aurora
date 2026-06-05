/*
Package coreHandler triển khai HTTP handlers cho Zone và Zone Service Management.

DESIGN CONTRACTS:
	1. Các thao tác thay đổi trạng thái (mutation) như update status và upsert service sử dụng thuần DTO body payload thay vì path parameter.
	2. Phân biệt rõ: GET dùng path parameter (như list services), POST/PATCH/PUT dùng request body.

SOURCE OF TRUTH:
	1. File route.go của core module là Source of Truth cho các handler trong file này.
	2. Struct DTO là Source of Truth cho input của handler.
	3. Entity của domain service là Source of Truth cho output mapping sang gin.H của handler.

SYSTEM BOUNDARY:
	1. Handler sẽ không biết domain knowledge
	2. Mọi logic validate req sẽ nằm ở handler : binding, validate cấu trúc , normalize req, trimspace ,...
	3. Mapping domain error sang http error response tương ứng
	4. Ủy quyền xử lý nghiệp vụ cho zoneSvc interface
*/

package coreHandler

import (
	"context"
	"errors"
	"strings"
	"time"

	coreEntity "controlplane/internal/core/domain/entity"
	coreSvcInterface "controlplane/internal/core/domain/service"
	coreErrorx "controlplane/internal/core/errorx"
	requestdto "controlplane/internal/core/transport/http/dto/req"
	apires "controlplane/pkg/apires"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type ZoneHandler struct {
	zoneSvc coreSvcInterface.ZoneService
}

func NewZoneHandler(zoneSvc coreSvcInterface.ZoneService) *ZoneHandler {
	return &ZoneHandler{zoneSvc: zoneSvc}
}

// CreateZone godoc
// @Summary      Create a new zone
// @Description  Create a new infrastructure zone with status fixed to "planned" and upsert all 5 zone services
// @Tags         zones
// @Accept       json
// @Produce      json
// @Param        request body requestdto.CreateZoneRequest true "Zone creation request"
// @Success      201 {object} map[string]interface{} "Zone created successfully"
// @Failure      400 {object} map[string]interface{} "Invalid request"
// @Failure      409 {object} map[string]interface{} "Zone code already exists"
// @Failure      500 {object} map[string]interface{} "Internal server error"
// @Router       /admin/zones [post]
// @Security     AdminAuth
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

	err := h.zoneSvc.CreateZone(ctx, coreEntity.CreateZoneInput{
		Code:             request.Code,
		Name:             request.Name,
		Location:         request.Location,
		EnableHypervisor: request.EnableHypervisor,
		EnableStorage:    request.EnableStorage,
		EnableMail:       request.EnableMail,
		EnableK8s:        request.EnableK8s,
		EnableAI:         request.EnableAI,
	})
	if err != nil {
		switch {
		case errors.Is(err, coreErrorx.ErrZoneInvalidInput):
			logger.HandlerWarn(c, op, err, "create zone invalid input")
			apires.RespondBadRequest(c, "invalid request")
		case errors.Is(err, coreErrorx.ErrZoneCodeAlreadyExists):
			logger.HandlerWarn(c, op, err, "create zone conflict")
			apires.RespondConflict(c, "resource already exists")
		case errors.Is(err, coreErrorx.ErrZoneServiceInvalidType):
			logger.HandlerWarn(c, op, err, "create zone invalid service type")
			apires.RespondBadRequest(c, "invalid request")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}
	apires.RespondCreated(c, nil, "zone created")
}

// GetZoneCatalog godoc
// @Summary      Get zone catalog
// @Description  Lightweight catalog of operational zones (active, draining, maintenance) for Select/Dropdown UI.
//
//	Returns only id, code, name — no sensitive status or timestamp fields.
//
// @Tags         zones
// @Produce      json
// @Success      200 {object} map[string]interface{} "Zone catalog fetched successfully"
// @Failure      500 {object} map[string]interface{} "Internal server error"
// @Router       /admin/core/zones/catalog [get]
// @Router       /api/v1/zones/catalog [get]
// @Security     UserAuth
func (h *ZoneHandler) GetZoneCatalog(c *gin.Context) {
	const op = "core.zone.catalog"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	items, err := h.zoneSvc.GetZoneCatalog(ctx)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal_error")
		return
	}
	rows := make([]gin.H, 0, len(items))
	for _, item := range items {
		rows = append(rows, gin.H{
			"id":   item.ID,
			"code": item.Code,
			"name": item.Name,
		})
	}
	apires.RespondSuccess(c, gin.H{"items": rows, "total": len(rows)}, "zone catalog fetched")
}

// ListZones godoc
// @Summary      List all zones
// @Description  Retrieve a list of all infrastructure zones in the system
// @Tags         zones
// @Produce      json
// @Success      200 {object} map[string]interface{} "Zones fetched successfully"
// @Failure      500 {object} map[string]interface{} "Internal server error"
// @Router       /admin/zones [get]
// @Security     AdminAuth
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
	rows := make([]gin.H, 0, len(items))
	for _, item := range items {
		rows = append(rows, gin.H{
			"id":         item.ID,
			"code":       item.Code,
			"name":       item.Name,
			"status":     string(item.Status),
			"created_at": item.CreatedAt,
			"updated_at": item.UpdatedAt,
		})
	}

	// trả về client toàn bộ zone đang có và số lượng zone
	apires.RespondSuccess(c, gin.H{"items": rows, "total": len(rows)}, "zones fetched")
}

// UpdateZoneStatus godoc
// @Summary      Update zone status
// @Description  Transition zone to a new status according to the state machine. Affects traffic routing immediately.
// @Tags         zones
// @Accept       json
// @Produce      json
// @Param        request body requestdto.UpdateZoneStatusRequest true "Status update request"
// @Success      200 {object} map[string]interface{} "Zone status updated successfully"
// @Failure      400 {object} map[string]interface{} "Invalid request or invalid status"
// @Failure      404 {object} map[string]interface{} "Zone not found"
// @Failure      409 {object} map[string]interface{} "Invalid state transition"
// @Failure      500 {object} map[string]interface{} "Internal server error"
// @Router       /admin/zones/status [patch]
// @Security     AdminAuth
func (h *ZoneHandler) UpdateZoneStatus(c *gin.Context) {
	const op = "core.zone.update_status"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	var request requestdto.UpdateZoneStatusRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.HandlerWarn(c, op, err, "bind update zone status request failed")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	toStatus := coreEntity.ZoneStatus(strings.ToLower(strings.TrimSpace(request.Status)))
	switch toStatus {
	case coreEntity.ZoneStatusPlanned, coreEntity.ZoneStatusActive, coreEntity.ZoneStatusDraining,
		coreEntity.ZoneStatusMaintenance, coreEntity.ZoneStatusDisabled:
	default:
		logger.HandlerWarn(c, op, nil, "update zone invalid status: "+request.Status)
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	zone, err := h.zoneSvc.UpdateZoneStatus(ctx, request.ZoneID, toStatus)
	if err != nil {
		switch {
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
	apires.RespondSuccess(c, gin.H{
		"id":         zone.ID,
		"code":       zone.Code,
		"name":       zone.Name,
		"status":     string(zone.Status),
		"created_at": zone.CreatedAt,
		"updated_at": zone.UpdatedAt,
	}, "zone status updated")
}

// DeleteZone godoc
// @Summary      Delete a zone
// @Description  Delete a zone when it meets all preconditions: disabled status, no dataplane nodes, no enabled services. Irreversible operation.
// @Tags         zones
// @Produce      json
// @Param        zone_id path string true "Zone ID (UUID)"
// @Success      200 {object} map[string]interface{} "Zone deleted successfully"
// @Failure      400 {object} map[string]interface{} "Invalid zone_id format"
// @Failure      404 {object} map[string]interface{} "Zone not found"
// @Failure      409 {object} map[string]interface{} "Delete preconditions not met"
// @Failure      500 {object} map[string]interface{} "Internal server error"
// @Router       /admin/zones/{zone_id} [delete]
// @Security     AdminAuth
func (h *ZoneHandler) DeleteZone(c *gin.Context) {
	const op = "core.zone.delete"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	zoneID := strings.TrimSpace(c.Param("zone_id"))
	parsedZoneID, parseErr := uuid.Parse(zoneID)
	if parseErr != nil {
		logger.HandlerWarn(c, op, parseErr, "delete zone invalid zone_id")
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	if err := h.zoneSvc.DeleteZone(ctx, parsedZoneID); err != nil {
		switch {
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

// ListZoneServices godoc
// @Summary      List zone services
// @Description  Retrieve all zone services (enabled/disabled status) for a specific zone
// @Tags         zone-services
// @Produce      json
// @Param        zone_id path string true "Zone ID (UUID)"
// @Success      200 {object} map[string]interface{} "Zone services fetched successfully"
// @Failure      400 {object} map[string]interface{} "Invalid zone_id format"
// @Failure      404 {object} map[string]interface{} "Zone not found"
// @Failure      500 {object} map[string]interface{} "Internal server error"
// @Router       /admin/zones/{zone_id}/services [get]
// @Security     AdminAuth
func (h *ZoneHandler) ListZoneServices(c *gin.Context) {
	const op = "core.zone_service.list"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	zoneID := strings.TrimSpace(c.Param("zone_id"))
	parsedZoneID, parseErr := uuid.Parse(zoneID)
	if parseErr != nil {
		logger.HandlerWarn(c, op, parseErr, "list zone services invalid zone_id")
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	items, err := h.zoneSvc.ListZoneServices(ctx, parsedZoneID)
	if err != nil {
		switch {
		case errors.Is(err, coreErrorx.ErrZoneServiceZoneNotFound):
			apires.RespondNotFound(c, "resource not found")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}
	rows := make([]gin.H, 0, len(items))
	for _, item := range items {
		rows = append(rows, gin.H{
			"id":           item.ID,
			"zone_id":      item.ZoneID,
			"service_type": string(item.ServiceType),
			"enabled":      item.Enabled,
			"created_at":   item.CreatedAt,
			"updated_at":   item.UpdatedAt,
		})
	}
	apires.RespondSuccess(c, gin.H{"items": rows, "total": len(rows)}, "zone services fetched")
}

// UpsertZoneService godoc
// @Summary      Update zone service status
// @Description  Enable or disable a specific service in a zone. Only allowed when zone is in maintenance status.
// @Tags         zone-services
// @Accept       json
// @Produce      json
// @Param        request body requestdto.UpsertZoneServiceRequest true "Service update request"
// @Success      200 {object} map[string]interface{} "Zone service updated successfully"
// @Failure      400 {object} map[string]interface{} "Invalid request or invalid service type"
// @Failure      404 {object} map[string]interface{} "Zone not found"
// @Failure      409 {object} map[string]interface{} "Zone not in maintenance status"
// @Failure      500 {object} map[string]interface{} "Internal server error"
// @Router       /admin/zones/services [put]
// @Security     AdminAuth
func (h *ZoneHandler) UpsertZoneService(c *gin.Context) {
	const op = "core.zone_service.upsert"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	var request requestdto.UpsertZoneServiceRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.HandlerWarn(c, op, err, "bind upsert zone service request failed")
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	parsedZoneID := request.ZoneID

	serviceType := coreEntity.ZoneServiceType(strings.ToLower(strings.TrimSpace(request.ServiceType)))

	// check lỗi validate service type
	switch serviceType {
	case coreEntity.ZoneServiceTypeHypervisor, coreEntity.ZoneServiceTypeStorage,
		coreEntity.ZoneServiceTypeMail, coreEntity.ZoneServiceTypeK8s, coreEntity.ZoneServiceTypeAI,
		coreEntity.ZoneServiceTypeDatabase:
	default:
		logger.HandlerWarn(c, op, nil, "upsert zone service invalid type: "+request.ServiceType)
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	item, err := h.zoneSvc.UpsertZoneService(ctx, parsedZoneID, serviceType, *request.Enabled)
	if err != nil {
		switch {
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
	apires.RespondSuccess(c, gin.H{
		"id":           item.ID,
		"zone_id":      item.ZoneID,
		"service_type": string(item.ServiceType),
		"enabled":      item.Enabled,
		"created_at":   item.CreatedAt,
		"updated_at":   item.UpdatedAt,
	}, "zone service updated")
}
