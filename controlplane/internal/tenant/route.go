package tenant

import (
	"controlplane/internal/http/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(router *gin.Engine, module *Module) {
	if router == nil || module == nil {
		return
	}
	router.POST("/api/v1/tenants", middleware.ACL(), module.TenantHandler.CreateTenant)
}
