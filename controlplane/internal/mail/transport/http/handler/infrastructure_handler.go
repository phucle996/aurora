package mailHandler

import (
	"context"
	"errors"
	"time"

	mailSvcInterface "controlplane/internal/mail/domain/service"
	mailTaxonomy "controlplane/internal/mail/taxonomy"
	apires "controlplane/pkg/apires"
	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
)

type InfrastructureHandler struct {
	svc mailSvcInterface.InfrastructureService
}

func NewInfrastructureHandler(svc mailSvcInterface.InfrastructureService) *InfrastructureHandler {
	return &InfrastructureHandler{svc: svc}
}

func (h *InfrastructureHandler) GetByZoneID(c *gin.Context) {
	// [COMMENT]: Gán operation tag chuẩn cho luồng truy vấn hạ tầng mail của admin
	const op = "mail.admin.infrastructure.get"
	
	// [COMMENT]: Khởi tạo context với timeout 5 giây để bảo vệ handler khỏi bị nghẽn DB
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: Trích xuất zone_id từ Gin Context (được middleware Gateway/Envoy parse từ Header X-Zone-Id)
	// Tự động ghi warning log và phản hồi HTTP 400 Bad Request chuẩn hóa nếu không tìm thấy hoặc sai định dạng.
	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}
	// [COMMENT]: Gọi domain service để lấy dữ liệu báo cáo hạ tầng mail theo zone_id
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
