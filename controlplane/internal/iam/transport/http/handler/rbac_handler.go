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
