package storageHandler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	storageSvcInterface "controlplane/internal/storage/domain/service"
	apires "controlplane/pkg/apires"
	"controlplane/pkg/constant"
)

// [COMMENT]: CredentialHandler quản lý các request HTTP tương tác với Access Keys của MinIO.
type CredentialHandler struct {
	tenantSvc   storageSvcInterface.TenantCredentialService
	personalSvc storageSvcInterface.PersonalCredentialService
}

// [COMMENT]: NewCredentialHandler khởi tạo controller quản lý key credentials.
func NewCredentialHandler(
	tenantSvc storageSvcInterface.TenantCredentialService,
	personalSvc storageSvcInterface.PersonalCredentialService,
) *CredentialHandler {
	return &CredentialHandler{
		tenantSvc:   tenantSvc,
		personalSvc: personalSvc,
	}
}

func (h *CredentialHandler) Create(c *gin.Context) {
	tenantIDStr := strings.TrimSpace(c.GetHeader(constant.HeaderXTenantID))
	// [COMMENT]: SKELETON - Đã định tuyến rẽ nhánh theo X-Tenant-ID
	if tenantIDStr == "" {
		_ = h.personalSvc // call personalSvc
	} else {
		_ = h.tenantSvc   // call tenantSvc
	}
	c.JSON(http.StatusNotImplemented, apires.APIResponse{Error: "not_implemented", Message: "API Create credential not implemented"})
}

func (h *CredentialHandler) Get(c *gin.Context) {
	tenantIDStr := strings.TrimSpace(c.GetHeader(constant.HeaderXTenantID))
	if tenantIDStr == "" {
		_ = h.personalSvc
	} else {
		_ = h.tenantSvc
	}
	c.JSON(http.StatusNotImplemented, apires.APIResponse{Error: "not_implemented", Message: "API Get credential not implemented"})
}

func (h *CredentialHandler) List(c *gin.Context) {
	tenantIDStr := strings.TrimSpace(c.GetHeader(constant.HeaderXTenantID))
	if tenantIDStr == "" {
		_ = h.personalSvc
	} else {
		_ = h.tenantSvc
	}
	c.JSON(http.StatusNotImplemented, apires.APIResponse{Error: "not_implemented", Message: "API List credentials not implemented"})
}

func (h *CredentialHandler) Revoke(c *gin.Context) {
	tenantIDStr := strings.TrimSpace(c.GetHeader(constant.HeaderXTenantID))
	if tenantIDStr == "" {
		_ = h.personalSvc
	} else {
		_ = h.tenantSvc
	}
	c.JSON(http.StatusNotImplemented, apires.APIResponse{Error: "not_implemented", Message: "API Revoke credential not implemented"})
}
