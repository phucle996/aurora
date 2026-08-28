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

// [COMMENT]: PersonalRenderContextHandler xử lý HTTP endpoint đọc render context cá nhân (/api/v1/personal/iam/context/read)
type PersonalRenderContextHandler struct {
	service iamSvcInterface.PersonalRenderContextService
}

// [COMMENT]: NewPersonalRenderContextHandler khởi tạo HTTP handler cho Personal Render Context
func NewPersonalRenderContextHandler(service iamSvcInterface.PersonalRenderContextService) *PersonalRenderContextHandler {
	return &PersonalRenderContextHandler{service: service}
}

// [COMMENT]: GetPersonalRenderContext trích xuất danh tính verified user và trả về projection capabilities/navigation cấp personal cho UI
func (h *PersonalRenderContextHandler) GetPersonalRenderContext(c *gin.Context) {
	const op = "iam.render_context.get_personal"
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
