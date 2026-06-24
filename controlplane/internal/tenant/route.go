package tenant

import (
	"github.com/gin-gonic/gin"
)

func RegisterRoutes(router *gin.Engine, module *Module) {
	if router == nil || module == nil {
		return
	}
	router.POST("/api/v1/tenants", module.TenantHandler.CreateTenant)
}
