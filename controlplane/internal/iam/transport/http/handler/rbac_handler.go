package iamHandler

import (
	iamSvcInterface "controlplane/internal/iam/domain/service"

	"github.com/gin-gonic/gin"
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
	c.JSON(200, gin.H{"message": "skeleton"})
}

// [COMMENT]: AssignTenantRole gán role và permissions cho tenant (skeleton HTTP handler)
func (h *RbacHandler) AssignTenantRole(c *gin.Context) {
	// [COMMENT]: Sẽ được hiện thực hóa ở phase tiếp theo
	c.JSON(200, gin.H{"message": "skeleton"})
}

// RoleResponse định nghĩa thông tin vai trò trả về cho API Client
type RoleResponse struct {
	ID        string `json:"id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	RoleLevel int    `json:"role_level"`
	Scope     string `json:"scope"`
}

// [COMMENT]: ListPlatformRoles trả về toàn bộ danh sách platform-scoped roles
func (h *RbacHandler) ListPlatformRoles(c *gin.Context) {
	ctx := c.Request.Context()
	roles, err := h.rbacSvc.ListPlatformRoles(ctx)
	if err != nil {
		c.JSON(500, gin.H{"error_message": err.Error()})
		return
	}

	resp := make([]RoleResponse, 0, len(roles))
	for _, r := range roles {
		resp = append(resp, RoleResponse{
			ID:        r.ID.String(),
			Code:      r.Code,
			Name:      r.Name,
			RoleLevel: r.RoleLevel,
			Scope:     r.Scope,
		})
	}

	c.JSON(200, resp)
}
