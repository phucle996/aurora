package mailHandler

import (
	"context"
	"errors"
	"strings"
	"time"

	mailSvcInterface "controlplane/internal/mail/domain/service"
	mailTaxonomy "controlplane/internal/mail/taxonomy"
	apires "controlplane/pkg/apires"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type InfrastructureHandler struct {
	svc mailSvcInterface.InfrastructureService
}

func NewInfrastructureHandler(svc mailSvcInterface.InfrastructureService) *InfrastructureHandler {
	return &InfrastructureHandler{svc: svc}
}

func (h *InfrastructureHandler) GetByZoneID(c *gin.Context) {
	const op = "mail.admin.infrastructure.get"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	zoneID, err := uuid.Parse(strings.TrimSpace(c.Param("zone_id")))
	if err != nil {
		apires.RespondBadRequest(c, "invalid zone_id")
		return
	}
	result, err := h.svc.GetByZoneID(ctx, zoneID)
	if err != nil {
		switch {
		case errors.Is(err, mailTaxonomy.ErrInvalidArgument):
			apires.RespondBadRequest(c, "invalid request")
		case errors.Is(err, mailTaxonomy.ErrInfrastructureNotFound):
			apires.RespondNotFound(c, "mail infrastructure not found")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	// [COMMENT]: Admin response chỉ trả sanitized current snapshot; không có management URL/token.
	apires.RespondSuccess(c, gin.H{
		"zone_id":             result.ZoneID.String(),
		"desired_state":       result.DesiredState,
		"actual_state":        result.ActualState,
		"service_state":       result.ServiceState,
		"fresh":               result.Fresh,
		"report_generation":   result.ReportGeneration,
		"report_sequence":     result.ReportSequence,
		"capacity":            result.Capacity,
		"pending_items":       result.PendingItems,
		"in_flight_batches":   result.InFlightBatches,
		"probe_node_id":       result.ProbeNodeID,
		"dataplane_nodes":     result.DataplaneNodes,
		"stalwart_nodes":      result.StalwartNodes,
		"inventory_truncated": result.InventoryTruncated,
		"error_code":          result.ErrorCode,
		"reported_at":         result.ReportedAt,
		"expires_at":          result.ExpiresAt,
	}, "mail infrastructure fetched")
}
