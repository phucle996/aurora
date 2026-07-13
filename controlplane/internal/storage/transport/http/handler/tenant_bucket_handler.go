package storageHandler

import (
	storageSvcInterface "controlplane/internal/storage/domain/service"
	"github.com/gin-gonic/gin"
)

// [COMMENT]: TenantBucketHandler xử lý các HTTP request quản trị Bucket cho Tenant.
// Tạm thời để trống theo yêu cầu thiết kế.
type TenantBucketHandler struct {
	tenantSvc storageSvcInterface.TenantBucketService
}

// [COMMENT]: NewTenantBucketHandler khởi tạo controller xử lý các endpoint Bucket cho Tenant.
func NewTenantBucketHandler(
	tenantSvc storageSvcInterface.TenantBucketService,
) *TenantBucketHandler {
	return &TenantBucketHandler{
		tenantSvc: tenantSvc,
	}
}

func (h *TenantBucketHandler) Create(c *gin.Context)      {}
func (h *TenantBucketHandler) Get(c *gin.Context)         {}
func (h *TenantBucketHandler) List(c *gin.Context)        {}
func (h *TenantBucketHandler) UpdateQuota(c *gin.Context) {}
func (h *TenantBucketHandler) Delete(c *gin.Context)      {}
