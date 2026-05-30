package mailHandler

import (
	mailSvcInterface "controlplane/internal/mail/domain/service"
	"github.com/gin-gonic/gin"
)

type GatewayHandler struct {
	svc mailSvcInterface.GatewayService
}

func NewGatewayHandler(svc mailSvcInterface.GatewayService) *GatewayHandler {
	return &GatewayHandler{svc: svc}
}

func (h *GatewayHandler) Create(c *gin.Context) {
	// Skeleton
}

func (h *GatewayHandler) Get(c *gin.Context) {
	// Skeleton
}

func (h *GatewayHandler) List(c *gin.Context) {
	// Skeleton
}

func (h *GatewayHandler) Update(c *gin.Context) {
	// Skeleton
}

func (h *GatewayHandler) Delete(c *gin.Context) {
	// Skeleton
}
