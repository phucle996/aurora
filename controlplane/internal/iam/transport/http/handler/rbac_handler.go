package iamHandler

import (
	"context"
	"strings"
	"time"

	iamSvcInterface "controlplane/internal/iam/domain/service"
	"controlplane/pkg/apires"
	"controlplane/pkg/constant"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// [COMMENT]: RbacHandler xử lý các HTTP endpoints cho IAM RBAC (dạng skeleton cho phase tiếp theo)
type RbacHandler struct {
	rbacSvc iamSvcInterface.RbacService
}

// [COMMENT]: NewRbacHandler khởi tạo HTTP handler tối giản cho RBAC
func NewRbacHandler(rbacSvc iamSvcInterface.RbacService) *RbacHandler {
	return &RbacHandler{rbacSvc: rbacSvc}
}

// [COMMENT]: AssignUserRole gán role và permissions cho user (skeleton HTTP handler)
func (h *RbacHandler) AssignUserRole(c *gin.Context) {
	// [COMMENT]: Sẽ được hiện thực hóa ở phase tiếp theo
	apires.RespondSuccess(c, gin.H{"message": "skeleton"}, "success")
}

// [COMMENT]: AssignTenantRole gán role và permissions cho tenant (skeleton HTTP handler)
func (h *RbacHandler) AssignTenantRole(c *gin.Context) {
	// [COMMENT]: Sẽ được hiện thực hóa ở phase tiếp theo
	apires.RespondSuccess(c, gin.H{"message": "skeleton"}, "success")
}

// [COMMENT]: ListPlatformRoles trả về toàn bộ danh sách platform-scoped roles bọc trong gin.H
func (h *RbacHandler) ListPlatformRoles(c *gin.Context) {

	const op = "iam.rbac.list_platform_roles"

	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	roles, err := h.rbacSvc.ListPlatformRoles(ctx)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal error occurred")
		return
	}

	resp := make([]gin.H, 0, len(roles))
	for _, r := range roles {
		resp = append(resp, gin.H{
			"id":         r.ID.String(),
			"code":       r.Code,
			"name":       r.Name,
			"role_level": r.RoleLevel,
			"scope":      r.Scope,
		})
	}

	apires.RespondSuccess(c, gin.H{"roles": resp}, "success")
}

// [COMMENT]: ListTenantRoles trả về danh sách roles được gán cho tenant cụ thể lấy từ header X-Tenant-ID
func (h *RbacHandler) ListTenantRoles(c *gin.Context) {

	const op = "iam.rbac.list_tenant_roles"
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	tenantIDStr := strings.TrimSpace(c.GetHeader(constant.HeaderXTenantID))
	if tenantIDStr == "" {
		logger.HandlerWarn(c, op, nil, "missing tenant context header X-Tenant-ID")
		apires.RespondBadRequest(c, "missing tenant context")
		return
	}

	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		logger.HandlerWarn(c, op, err, "invalid tenant id format")
		apires.RespondBadRequest(c, "invalid tenant id format")
		return
	}

	roles, err := h.rbacSvc.ListTenantRoles(ctx, tenantID)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal error occurred")
		return
	}

	resp := make([]gin.H, 0, len(roles))
	for _, r := range roles {
		resp = append(resp, gin.H{
			"id":         r.ID.String(),
			"code":       r.Code,
			"name":       r.Name,
			"role_level": r.RoleLevel,
			"scope":      r.Scope,
		})
	}

	apires.RespondSuccess(c, gin.H{"roles": resp}, "success")
}

// [COMMENT]: GetRenderContext trả về cấu hình Navigation và Capabilities cho console UI dựa theo user id
func (h *RbacHandler) GetRenderContext(c *gin.Context) {
	const op = "iam.rbac.get_render_context"
	ctx, cancel := context.WithTimeout(constant.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: Lấy userID trực tiếp từ x-user-id header do Edge Gateway/acr chuyển tiếp xuống
	userIDStr := strings.TrimSpace(c.GetHeader("x-user-id"))
	if userIDStr == "" {
		logger.HandlerWarn(c, op, nil, "unauthorized - missing x-user-id header")
		apires.RespondUnauthorized(c, "unauthorized")
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		logger.HandlerWarn(c, op, err, "invalid user id format")
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	renderContext, err := h.rbacSvc.GetRenderContext(ctx, userID)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal error occurred")
		return
	}

	type navItemDTO struct {
		Key     string   `json:"key"`
		Actions []string `json:"actions"`
	}
	navDTOs := make([]navItemDTO, 0, len(renderContext.Navigation))
	for _, nav := range renderContext.Navigation {
		navDTOs = append(navDTOs, navItemDTO{
			Key:     nav.Key,
			Actions: nav.Actions,
		})
	}

	apires.RespondSuccess(c, gin.H{
		"navigation":   navDTOs,
		"capabilities": renderContext.Capabilities,
	}, "success")
}

