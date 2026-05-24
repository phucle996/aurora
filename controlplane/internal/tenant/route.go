package tenant

import (
	"controlplane/internal/http/middleware"

	"controlplane/internal/security"
	"github.com/gin-gonic/gin"
	goredis "github.com/redis/go-redis/v9"
)

func RegisterRoutes(router *gin.Engine, module *Module, sp security.SecretProvider, rdb *goredis.Client) {
	if router == nil || module == nil {
		return
	}
	router.POST("/api/v1/tenants", middleware.Access(), module.TenantHandler.CreateTenant)
}
