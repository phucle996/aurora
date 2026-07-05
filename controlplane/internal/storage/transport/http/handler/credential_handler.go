package handler

import (
	"net/http"

	response "controlplane/pkg/apires"
	storageSvc "controlplane/internal/storage/domain/service"

	"github.com/gin-gonic/gin"
)

// [COMMENT]: CredentialHandler quản lý các request HTTP tương tác với Access Keys của MinIO.
type CredentialHandler struct {
	svc storageSvc.CredentialService
}

// [COMMENT]: NewCredentialHandler khởi tạo controller quản lý key credentials.
func NewCredentialHandler(svc storageSvc.CredentialService) *CredentialHandler {
	return &CredentialHandler{
		svc: svc,
	}
}

func (h *CredentialHandler) Create(c *gin.Context) {
	// [COMMENT]: SKELETON - Chưa cài đặt logic HTTP handler.
	c.JSON(http.StatusNotImplemented, response.APIResponse{Error: "not_implemented", Message: "API Create credential not implemented"})
}

func (h *CredentialHandler) Get(c *gin.Context) {
	// [COMMENT]: SKELETON - Chưa cài đặt logic HTTP handler.
	c.JSON(http.StatusNotImplemented, response.APIResponse{Error: "not_implemented", Message: "API Get credential not implemented"})
}

func (h *CredentialHandler) List(c *gin.Context) {
	// [COMMENT]: SKELETON - Chưa cài đặt logic HTTP handler.
	c.JSON(http.StatusNotImplemented, response.APIResponse{Error: "not_implemented", Message: "API List credentials not implemented"})
}

func (h *CredentialHandler) Revoke(c *gin.Context) {
	// [COMMENT]: SKELETON - Chưa cài đặt logic HTTP handler.
	c.JSON(http.StatusNotImplemented, response.APIResponse{Error: "not_implemented", Message: "API Revoke credential not implemented"})
}
