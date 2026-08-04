package iamHandler

import (
	"context"
	"errors"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/pkg/apires"
	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
)

type RenderContextHandler struct {
	service iamSvcInterface.RenderContextService
}

func NewRenderContextHandler(service iamSvcInterface.RenderContextService) *RenderContextHandler {
	return &RenderContextHandler{service: service}
}

func (h *RenderContextHandler) GetPersonalRenderContext(c *gin.Context) {
	const op = "iam.render_context.personal.read"
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	if _, tenantContext := c.Get(pkgcontext.CtxTenantID); tenantContext {
		apires.RespondForbidden(c, "personal context is unavailable")
		return
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	workflow := &iamEntity.PersonalRenderContext{UserID: userID}
	if err := h.service.GetPersonalRenderContext(ctx, workflow); err != nil {
		if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			apires.RespondForbidden(c, "render context is unavailable")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondServiceUnavailable(c, "render context is temporarily unavailable")
		return
	}

	navigation := make([]gin.H, 0, len(workflow.NavigationKeys))
	currentKey := ""
	currentActions := make([]string, 0, 4)
	for index, key := range workflow.NavigationKeys {
		if currentKey != "" && currentKey != key {
			navigation = append(navigation, gin.H{"key": currentKey, "actions": currentActions})
			currentActions = make([]string, 0, 4)
		}
		currentKey = key
		currentActions = append(currentActions, workflow.NavigationActions[index])
	}
	if currentKey != "" {
		navigation = append(navigation, gin.H{"key": currentKey, "actions": currentActions})
	}
	capabilities := make(gin.H, len(workflow.Capabilities))
	for _, permission := range workflow.Capabilities {
		capabilities[permission] = true
	}
	apires.RespondSuccess(c, gin.H{
		"kind":         "personal",
		"navigation":   navigation,
		"capabilities": capabilities,
	}, "success")
}

func (h *RenderContextHandler) GetTenantRenderContext(c *gin.Context) {
	const op = "iam.render_context.tenant.read"
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	tenantID, ok := pkgcontext.GetTenantID(c, op)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	workflow := &iamEntity.TenantRenderContext{UserID: userID, TenantID: tenantID}
	if err := h.service.GetTenantRenderContext(ctx, workflow); err != nil {
		if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			apires.RespondForbidden(c, "render context is unavailable")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondServiceUnavailable(c, "render context is temporarily unavailable")
		return
	}

	navigation := make([]gin.H, 0, len(workflow.NavigationKeys))
	currentKey := ""
	currentActions := make([]string, 0, 4)
	for index, key := range workflow.NavigationKeys {
		if currentKey != "" && currentKey != key {
			navigation = append(navigation, gin.H{"key": currentKey, "actions": currentActions})
			currentActions = make([]string, 0, 4)
		}
		currentKey = key
		currentActions = append(currentActions, workflow.NavigationActions[index])
	}
	if currentKey != "" {
		navigation = append(navigation, gin.H{"key": currentKey, "actions": currentActions})
	}
	capabilities := make(gin.H, len(workflow.Capabilities))
	for _, permission := range workflow.Capabilities {
		capabilities[permission] = true
	}
	apires.RespondSuccess(c, gin.H{
		"kind":         "tenant",
		"tenant_id":    tenantID.String(),
		"navigation":   navigation,
		"capabilities": capabilities,
	}, "success")
}
