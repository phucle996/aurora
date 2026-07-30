package handler

import (
	"context"
	"strconv"
	"strings"
	"time"

	"controlplane/internal/managedservice/domain/entity"
	managedservice "controlplane/internal/managedservice/domain/service"
	"controlplane/pkg/apires"
	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
)

type AuditHandler struct {
	service managedservice.AuditService
}

func NewAuditHandler(service managedservice.AuditService) *AuditHandler {
	return &AuditHandler{service: service}
}

func (h *AuditHandler) ListAuditEvents(c *gin.Context) {
	const op = "managedservice.audit.list"

	// [COMMENT]: Chỉ SRE mới được phép truy cập audit log.
	if strings.TrimSpace(c.GetHeader("x-user-id")) != "sre" {
		apires.RespondForbidden(c, "forbidden")
		return
	}

	// [COMMENT]: Giới hạn limit mặc định 50, tối đa 100 để bảo vệ DB.
	limit := 50
	if rawLimit := strings.TrimSpace(c.Query("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed < 1 || parsed > 100 {
			apires.RespondBadRequest(c, "limit must be between 1 and 100")
			return
		}
		limit = parsed
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	result, err := h.service.ListAuditEvents(ctx, &entity.ListAuditEvents{Limit: limit})
	if err != nil {
		logger.HandlerError(c, "managedservice.audit.list", err)
		apires.RespondInternalError(c, "SRE_CATALOG_INTERNAL")
		return
	}

	// [COMMENT]: Map từng audit event sang response shape nhẹ, không expose internal fields.
	items := make([]gin.H, 0, len(result))
	for _, item := range result {
		items = append(items, gin.H{
			"id":                item.ID,
			"actor":             item.ActorSubject,
			"critical_proof_id": item.CriticalProofID,
			"action":            item.Action,
			"record_kind":       item.RecordKind,
			"record_id":         item.RecordID,
			"record_version":    item.RecordVersion,
			"outcome":           item.Outcome,
			"error_code":        item.ErrorCode,
			"occurred_at":       item.OccurredAt,
		})
	}

	apires.RespondSuccess(c, gin.H{"items": items}, "audit events fetched")
}
