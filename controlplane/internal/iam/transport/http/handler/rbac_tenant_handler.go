package iamHandler

import (
	"context"
	"time"

	iamSvcInterface "controlplane/internal/iam/domain/service"
	"controlplane/pkg/apires"
	"controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
)

// [COMMENT]: RbacTenantHandler xử lý các HTTP endpoints liên quan đến vai trò trong phạm vi Tenant
type RbacTenantHandler struct {
	rbacTenantSvc iamSvcInterface.RbacTenantService
}

// [COMMENT]: NewRbacTenantHandler khởi tạo HTTP handler cho Tenant RBAC
func NewRbacTenantHandler(rbacTenantSvc iamSvcInterface.RbacTenantService) *RbacTenantHandler {
	return &RbacTenantHandler{rbacTenantSvc: rbacTenantSvc}
}

// [COMMENT]: ListRolesTenant trả về danh sách roles của một tenant cụ thể (dựa vào header X-Tenant-ID)
func (h *RbacTenantHandler) ListRolesTenant(c *gin.Context) {
	const op = "iam.rbac.list_tenant_roles"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	tenantID, ok := pkgcontext.GetTenantID(c, op)
	if !ok {
		return
	}

	roles, err := h.rbacTenantSvc.ListTenantRoles(ctx, tenantID)
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

// [COMMENT]: AssignTenantRole gán role trong phạm vi tenant cho tenant con (skeleton)
func (h *RbacTenantHandler) AssignTenantRole(c *gin.Context) {
	// [COMMENT]: Sẽ hiện thực hóa ở phase tiếp theo
	apires.RespondSuccess(c, gin.H{"message": "skeleton tenant sub-tenant role assign"}, "success")
}
