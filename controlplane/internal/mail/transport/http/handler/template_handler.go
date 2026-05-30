package mailHandler

import (
	mailSvcInterface "controlplane/internal/mail/domain/service"
	"github.com/gin-gonic/gin"
)

type TemplateHandler struct {
	svc mailSvcInterface.TemplateService
}

func NewTemplateHandler(svc mailSvcInterface.TemplateService) *TemplateHandler {
	return &TemplateHandler{svc: svc}
}

func (h *TemplateHandler) Create(c *gin.Context) {
	// Skeleton
}

func (h *TemplateHandler) Get(c *gin.Context) {
	// Skeleton
}

func (h *TemplateHandler) List(c *gin.Context) {
	// Skeleton
}

func (h *TemplateHandler) Update(c *gin.Context) {
	// Skeleton
}

func (h *TemplateHandler) Delete(c *gin.Context) {
	// Skeleton
}
