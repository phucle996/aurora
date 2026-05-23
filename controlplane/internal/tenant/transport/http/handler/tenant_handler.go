package tenantHandler

import (
	"context"
	"errors"
	"time"

	tenantEntity "controlplane/internal/tenant/domain/entity"
	tenantSvc "controlplane/internal/tenant/domain/service"
	tenantErrorx "controlplane/internal/tenant/errorx"
	requestdto "controlplane/internal/tenant/transport/http/dto/req"
	"controlplane/internal/http/middleware"
	"controlplane/pkg/apires"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	svc tenantSvc.Service
}

func NewHandler(svc tenantSvc.Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) CreateTenant(c *gin.Context) {
	const op = "tenant.create"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	var req requestdto.CreateTenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.HandlerWarn(c, op, err, "invalid request")
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	creatorID := middleware.GetUserID(c)
	result, err := h.svc.CreateTenant(ctx, tenantEntity.CreateTenantInput{Name: req.Name, Domain: req.Domain, CreatorID: creatorID})
	if err != nil {
		switch {
		case errors.Is(err, tenantErrorx.ErrInvalidArgument):
			apires.RespondBadRequest(c, "invalid request")
		case errors.Is(err, tenantErrorx.ErrConflict):
			apires.RespondConflict(c, "resource already exists")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}
	apires.RespondCreated(c, gin.H{"tenant_id": result.TenantID, "domain": result.Domain}, "tenant created")
}
