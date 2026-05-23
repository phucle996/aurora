package tenant

import (
	"controlplane/internal/http/middleware"

	"github.com/gin-gonic/gin"
	goredis "github.com/redis/go-redis/v9"
	"controlplane/internal/security"
)

func RegisterRoutes(router *gin.Engine, module *Module, sp security.SecretProvider, rdb *goredis.Client) {
	if router == nil || module == nil { return }
	router.POST("/api/v1/tenants", middleware.Access(sp, rdb), module.TenantHandler.CreateTenant)
}
