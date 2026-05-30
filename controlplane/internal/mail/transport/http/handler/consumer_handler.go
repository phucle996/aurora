package mailHandler

import (
	mailSvcInterface "controlplane/internal/mail/domain/service"
	"github.com/gin-gonic/gin"
)

type ConsumerHandler struct {
	svc mailSvcInterface.ConsumerService
}

func NewConsumerHandler(svc mailSvcInterface.ConsumerService) *ConsumerHandler {
	return &ConsumerHandler{svc: svc}
}

func (h *ConsumerHandler) Create(c *gin.Context) {
	// Skeleton
}

func (h *ConsumerHandler) Get(c *gin.Context) {
	// Skeleton
}

func (h *ConsumerHandler) List(c *gin.Context) {
	// Skeleton
}

func (h *ConsumerHandler) Update(c *gin.Context) {
	// Skeleton
}

func (h *ConsumerHandler) Delete(c *gin.Context) {
	// Skeleton
}

func (h *ConsumerHandler) UpdateStatus(c *gin.Context) {
	// Skeleton
}

func (h *ConsumerHandler) TestConnection(c *gin.Context) {
	// Skeleton
}
