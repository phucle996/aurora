package hierarchyHandler

import (
	"context"
	"errors"
	"strings"
	"time"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	hierarchySvcInterface "controlplane/internal/hierarchy/domain/service"
	hierarchyTaxonomy "controlplane/internal/hierarchy/taxonomy"
	hierarchyReq "controlplane/internal/hierarchy/transport/http/dto/req"
	apires "controlplane/pkg/apires"
	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type ZoneHandler struct {
	zoneSvc hierarchySvcInterface.ZoneService
}

func NewZoneHandler(zoneSvc hierarchySvcInterface.ZoneService) *ZoneHandler {
	return &ZoneHandler{
		zoneSvc: zoneSvc,
	}
}

// CreateZone godoc
// @Summary      Create a new zone
// @Description  Create a new infrastructure zone with status fixed to "planned" and upsert all supported zone services
// @Tags         zones
// @Accept       json
// @Produce      json
// @Param        request body hierarchyReq.CreateZoneRequest true "Zone creation request"
// @Success      201 {object} map[string]interface{} "Zone created successfully"
// @Failure      400 {object} map[string]interface{} "Invalid request"
// @Failure      409 {object} map[string]interface{} "Zone code already exists"
// @Failure      500 {object} map[string]interface{} "Internal server error"
// @Router       /admin/critical/hierarchy/zones [post]
// @Security     AdminAuth
func (h *ZoneHandler) CreateZone(c *gin.Context) {
	const op = "hierarchy.zone.create"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	var request hierarchyReq.CreateZoneRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.HandlerWarn(c, op, err, "bind create zone request failed")
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	zoneCode := strings.ToLower(strings.TrimSpace(request.Code))
	zoneName := strings.TrimSpace(request.Name)
	zoneLocation := strings.TrimSpace(request.Location)
	if zoneCode == "" || zoneName == "" || zoneLocation == "" {
		logger.HandlerWarn(c, op, nil, "create zone normalized input is empty")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	enableHypervisor := request.EnableHypervisor != nil && *request.EnableHypervisor
	enableStorage := request.EnableStorage != nil && *request.EnableStorage
	enableMail := request.EnableMail != nil && *request.EnableMail
	enableKubernetes := request.EnableKubernetes != nil && *request.EnableKubernetes
	enableAI := request.EnableAI != nil && *request.EnableAI
	enableManagedService := request.EnableManagedService != nil && *request.EnableManagedService
	_, err := h.zoneSvc.CreateZone(ctx, &hierarchyEntity.CreateZone{
		Code:                 zoneCode,
		Name:                 zoneName,
		Location:             zoneLocation,
		Description:          strings.TrimSpace(request.Description),
		EnableHypervisor:     enableHypervisor,
		EnableStorage:        enableStorage,
		EnableMail:           enableMail,
		EnableKubernetes:     enableKubernetes,
		EnableAI:             enableAI,
		EnableManagedService: enableManagedService,
	})
	if err != nil {
		switch {
		case errors.Is(err, hierarchyTaxonomy.ErrAlreadyExists):
			logger.HandlerWarn(c, op, err, "create zone conflict")
			apires.RespondConflict(c, "resource already exists")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}
	apires.RespondCreated(c, nil, "zone created")
}

// ListZones godoc
// @Summary      List all zones
// @Description  Retrieve a list of all infrastructure zones in the system
// @Tags         zones
// @Produce      json
// @Success      200 {object} map[string]interface{} "Zones fetched successfully"
// @Failure      500 {object} map[string]interface{} "Internal server error"
// @Router       /admin/hierarchy/zones [get]
// @Security     AdminAuth
func (h *ZoneHandler) ListZones(c *gin.Context) {
	const op = "hierarchy.zone.list"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	items, err := h.zoneSvc.ListZones(ctx, &hierarchyEntity.ListZones{})
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
			"location":   item.Location,
			"status":     string(item.Status),
			"updated_at": item.UpdatedAt,
		})
	}

	// trả về client toàn bộ zone đang có và số lượng zone
	apires.RespondSuccess(c, gin.H{"items": rows,
		"total": len(rows)},
		"zones fetched")
}

// GetZone godoc
// @Summary      Get a zone detail
// @Description  Retrieve detailed information about a single infrastructure zone by ID
// @Tags         zones
// @Produce      json
// @Param        zone_id path string true "Zone ID (UUID)"
// @Success      200 {object} map[string]interface{} "Zone detail fetched successfully"
// @Failure      400 {object} map[string]interface{} "Invalid zone_id format"
// @Failure      404 {object} map[string]interface{} "Zone not found"
// @Failure      500 {object} map[string]interface{} "Internal server error"
// @Router       /admin/hierarchy/zones/{zone_id} [get]
// @Security     AdminAuth
func (h *ZoneHandler) GetDetailZone(c *gin.Context) {
	const op = "hierarchy.zone.get"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	zoneID, err := uuid.Parse(strings.TrimSpace(c.Param("zone_id")))
	if err != nil {
		logger.HandlerWarn(c, op, err, "get zone invalid zone_id format")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	detail, err := h.zoneSvc.GetZoneDetail(ctx, &hierarchyEntity.GetZoneDetail{ZoneID: zoneID})
	if err != nil {
		if errors.Is(err, hierarchyTaxonomy.ErrNotFound) {
			apires.RespondNotFound(c, "zone not found")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal_error")
		return
	}

	var enabledCount int
	serviceRows := make([]gin.H, 0)
	for _, item := range detail {
		if !item.HasService {
			continue
		}
		// [COMMENT]: Kiểm tra tính hợp lệ của loại service
		switch item.ServiceType {
		case hierarchyEntity.ZoneServiceTypeHypervisor,
			hierarchyEntity.ZoneServiceTypeStorage,
			hierarchyEntity.ZoneServiceTypeMail,
			hierarchyEntity.ZoneServiceTypeKubernetes,
			hierarchyEntity.ZoneServiceTypeAI,
			hierarchyEntity.ZoneServiceTypeDatabase,
			hierarchyEntity.ZoneServiceTypeManagedService:
			// Valid
		default:
			continue
		}

		// [COMMENT]: desiredStateString biểu diễn trạng thái mong muốn: enable hoặc disable
		desiredStateString := "disable"
		if item.DesiredState {
			desiredStateString = "enable"
			enabledCount++
		}

		// [COMMENT]: actualStateString biểu diễn trạng thái vận hành thực tế
		actualStateString := item.ActualState
		if actualStateString == "" {
			actualStateString = "unknown"
		}

		key := string(item.ServiceType)
		label := string(item.ServiceType)
		switch key {
		case "hypervisor":
			label = "Hypervisor"
		case "storage":
			label = "Storage"
		case "mail":
			label = "Mail"
		case "kubernetes":
			label = "Kubernetes"
		case "ai":
			label = "AI"
		case "database":
			label = "Database"
		case "managed_service":
			label = "Managed Services"
		}

		serviceRows = append(serviceRows, gin.H{
			"key":           key,
			"label":         label,
			"desired_state": desiredStateString,
			"actual_state":  actualStateString,
		})
	}

	zone := detail[0]
	apires.RespondSuccess(c, gin.H{
		"zone": gin.H{
			"id":          zone.ZoneID,
			"code":        zone.ZoneCode,
			"name":        zone.ZoneName,
			"location":    zone.ZoneLocation,
			"description": zone.ZoneDescription,
			"status":      string(zone.ZoneStatus),
			"created_at":  zone.ZoneCreatedAt,
			"updated_at":  zone.ZoneUpdatedAt,
		},
		"summary": gin.H{
			"workspaces":       0,
			"enabled_services": enabledCount,
		},
		"enabled_services": serviceRows,
		"workspaces": gin.H{
			"items":  []interface{}{},
			"total":  0,
			"limit":  5,
			"offset": 0,
		},
		"recent_activity": []interface{}{},
	}, "zone details fetched")
}

// UpdateZoneStatus godoc
// @Summary      Update zone status
// @Description  Transition zone to a new status according to the state machine. Affects traffic routing immediately.
// @Tags         zones
// @Accept       json
// @Produce      json
// @Param        request body hierarchyReq.UpdateZoneStatusRequest true "Status update request"
// @Success      200 {object} map[string]interface{} "Zone status updated successfully"
// @Failure      400 {object} map[string]interface{} "Invalid request or invalid status"
// @Failure      404 {object} map[string]interface{} "Zone not found"
// @Failure      409 {object} map[string]interface{} "Invalid state transition"
// @Failure      500 {object} map[string]interface{} "Internal server error"
// @Router       /admin/critical/hierarchy/zones/{zone_id}/status [patch]
// @Security     AdminAuth
func (h *ZoneHandler) UpdateZoneStatus(c *gin.Context) {
	const op = "hierarchy.zone.update_status"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	parsedZoneID, err := uuid.Parse(c.Param("zone_id"))
	if err != nil {
		logger.HandlerWarn(c, op, err, "parse zone_id uuid failed")
		apires.RespondBadRequest(c, "invalid zone_id format")
		return
	}

	var request hierarchyReq.UpdateZoneStatusRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.HandlerWarn(c, op, err, "bind update zone status request failed")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	toStatus := hierarchyEntity.ZoneStatus(strings.ToLower(strings.TrimSpace(request.Status)))
	switch toStatus {
	case hierarchyEntity.ZoneStatusPlanned, hierarchyEntity.ZoneStatusActive, hierarchyEntity.ZoneStatusDraining,
		hierarchyEntity.ZoneStatusMaintenance, hierarchyEntity.ZoneStatusDisabled:
	default:
		logger.HandlerWarn(c, op, nil, "update zone invalid status: "+request.Status)
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	_, err = h.zoneSvc.UpdateZoneStatus(ctx, &hierarchyEntity.UpdateZoneStatus{ZoneID: parsedZoneID, Status: toStatus})
	if err != nil {
		switch {
		case errors.Is(err, hierarchyTaxonomy.ErrNotFound):
			logger.HandlerWarn(c, op, err, "zone not found")
			apires.RespondNotFound(c, "resource not found")
		case errors.Is(err, hierarchyTaxonomy.ErrInvalidTransition):
			logger.HandlerWarn(c, op, err, "zone invalid transition")
			apires.RespondConflict(c, "state conflict")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}
	apires.RespondSuccess(c, nil, "zone status updated")
}

// DeleteZone godoc
// @Summary      Delete a zone
// @Description  Delete a zone when it meets all preconditions: disabled status and no enabled services. Irreversible operation.
// @Tags         zones
// @Produce      json
// @Param        zone_id path string true "Zone ID (UUID)"
// @Success      200 {object} map[string]interface{} "Zone deleted successfully"
// @Failure      400 {object} map[string]interface{} "Invalid zone_id format"
// @Failure      404 {object} map[string]interface{} "Zone not found"
// @Failure      409 {object} map[string]interface{} "Delete preconditions not met"
// @Failure      500 {object} map[string]interface{} "Internal server error"
// @Router       /admin/critical/hierarchy/zones/{zone_id} [delete]
// @Security     AdminAuth
func (h *ZoneHandler) DeleteZone(c *gin.Context) {
	const op = "hierarchy.zone.delete"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	zoneID := strings.TrimSpace(c.Param("zone_id"))
	parsedZoneID, parseErr := uuid.Parse(zoneID)
	if parseErr != nil {
		logger.HandlerWarn(c, op, parseErr, "delete zone invalid zone_id")
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	if _, err := h.zoneSvc.DeleteZone(ctx, &hierarchyEntity.DeleteZone{ZoneID: parsedZoneID}); err != nil {
		switch {
		case errors.Is(err, hierarchyTaxonomy.ErrNotFound):
			logger.HandlerWarn(c, op, err, "zone not found")
			apires.RespondNotFound(c, "resource not found")
		case errors.Is(err, hierarchyTaxonomy.ErrPreconditionFailed):
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

// UpsertZoneService godoc
// @Summary      Update zone service status
// @Description  Enable or disable a specific service in a zone. Only allowed when zone is in maintenance status.
// @Tags         zone-services
// @Accept       json
// @Produce      json
// @Param        request body hierarchyReq.UpsertZoneServiceRequest true "Service update request"
// @Success      200 {object} map[string]interface{} "Zone service updated successfully"
// @Failure      400 {object} map[string]interface{} "Invalid request or invalid service type"
// @Failure      404 {object} map[string]interface{} "Zone not found"
// @Failure      409 {object} map[string]interface{} "Zone not in maintenance status"
// @Failure      500 {object} map[string]interface{} "Internal server error"
// @Router       /admin/critical/hierarchy/zones/services [put]
// @Security     AdminAuth
func (h *ZoneHandler) UpdateZoneService(c *gin.Context) {
	const op = "hierarchy.zone_service.update"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	var request hierarchyReq.UpsertZoneServiceRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.HandlerWarn(c, op, err, "bind update zone service request failed")
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	parsedZoneID := request.ZoneID

	serviceType := hierarchyEntity.ZoneServiceType(strings.ToLower(strings.TrimSpace(request.ServiceType)))

	// check lỗi validate service type
	switch serviceType {
	case hierarchyEntity.ZoneServiceTypeHypervisor, hierarchyEntity.ZoneServiceTypeStorage,
		hierarchyEntity.ZoneServiceTypeMail, hierarchyEntity.ZoneServiceTypeKubernetes, hierarchyEntity.ZoneServiceTypeAI,
		hierarchyEntity.ZoneServiceTypeDatabase, hierarchyEntity.ZoneServiceTypeManagedService:
	default:
		logger.HandlerWarn(c, op, nil, "update zone service invalid type: "+request.ServiceType)
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	item, err := h.zoneSvc.UpdateZoneService(ctx, &hierarchyEntity.UpdateZoneService{
		ZoneID: parsedZoneID, ServiceType: serviceType, DesiredState: *request.Enabled,
	})
	if err != nil {
		switch {
		case errors.Is(err, hierarchyTaxonomy.ErrNotFound):
			apires.RespondNotFound(c, "resource not found")
		case errors.Is(err, hierarchyTaxonomy.ErrPreconditionFailed):
			apires.RespondConflict(c, "state conflict")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}
	apires.RespondSuccess(c, gin.H{
		"id":            item.ID,
		"zone_id":       item.ZoneID,
		"service_type":  string(item.ServiceType),
		"desired_state": item.DesiredState,
		"actual_state":  item.ActualState,
		"created_at":    item.CreatedAt,
		"updated_at":    item.UpdatedAt,
	}, "zone service updated")
}
