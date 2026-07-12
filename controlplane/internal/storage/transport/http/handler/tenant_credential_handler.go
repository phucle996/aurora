package storageHandler

import (
	storageSvcInterface "controlplane/internal/storage/domain/service"
	"github.com/gin-gonic/gin"
)

// [COMMENT]: TenantCredentialHandler quản lý các request HTTP tương tác với Access Keys của MinIO cho Tenant.
// Tạm thời để trống theo yêu cầu thiết kế.
type TenantCredentialHandler struct {
	tenantSvc storageSvcInterface.TenantCredentialService
}

// [COMMENT]: NewTenantCredentialHandler khởi tạo controller quản lý key credentials cho Tenant.
func NewTenantCredentialHandler(
	tenantSvc storageSvcInterface.TenantCredentialService,
) *TenantCredentialHandler {
	return &TenantCredentialHandler{
		tenantSvc: tenantSvc,
	}
}

func (h *TenantCredentialHandler) Create(c *gin.Context) {}
func (h *TenantCredentialHandler) Get(c *gin.Context)    {}
func (h *TenantCredentialHandler) List(c *gin.Context)   {}
func (h *TenantCredentialHandler) Delete(c *gin.Context) {}
