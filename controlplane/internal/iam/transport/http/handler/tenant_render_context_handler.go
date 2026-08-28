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

// [COMMENT]: TenantRenderContextHandler xử lý HTTP endpoint đọc render context trong ngữ cảnh Tenant (/api/v1/tenant/iam/context/read)
type TenantRenderContextHandler struct {
	service iamSvcInterface.TenantRenderContextService
}

// [COMMENT]: NewTenantRenderContextHandler khởi tạo HTTP handler cho Tenant Render Context
func NewTenantRenderContextHandler(service iamSvcInterface.TenantRenderContextService) *TenantRenderContextHandler {
	return &TenantRenderContextHandler{service: service}
}

// [COMMENT]: GetTenantRenderContext trích xuất user ID và tenant ID đã xác thực, sau đó trả về danh sách capabilities/navigation của tenant cho UI
func (h *TenantRenderContextHandler) GetTenantRenderContext(c *gin.Context) {
	const op = "iam.render_context.get_tenant"
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
