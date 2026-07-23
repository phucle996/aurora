package mail

import (
	"controlplane/internal/http/middleware"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes nhận path đã được Envoy/ACR rewrite sang đúng nhánh Personal hoặc Tenant,
// giống IAM. Handler không tự suy luận scope từ body và repository vẫn fail-close bằng DB scope check.
func RegisterRoutes(router *gin.Engine, module *Module) {
	if router == nil || module == nil {
		return
	}

	personal := router.Group("/api/v1/personal/mail")
	{
		// [COMMENT]: `email` là RBAC capability; `mail` trong URL chỉ là transport namespace tương thích.
		personal.POST("/consumers", middleware.Authorize("email:consumer:create", module.L1Registry, "*"), module.PersonalConsumerHandler.Create)
		personal.GET("/consumers", middleware.Authorize("email:consumer:read", module.L1Registry, "*"), module.PersonalConsumerHandler.List)
		personal.GET("/consumers/:id", middleware.Authorize("email:consumer:read", module.L1Registry, "*"), module.PersonalConsumerHandler.Get)
		personal.POST("/consumers/:id/runtime/watch", middleware.Authorize("email:consumer:read", module.L1Registry, "*"), module.PersonalConsumerHandler.WatchRuntime)
		personal.PATCH("/consumers/:id", middleware.Authorize("email:consumer:update", module.L1Registry, "*"), module.PersonalConsumerHandler.Update)
		personal.POST("/consumers/:id/pause", middleware.Authorize("email:consumer:update", module.L1Registry, "*"), module.PersonalConsumerHandler.Pause)
		personal.POST("/consumers/:id/resume", middleware.Authorize("email:consumer:update", module.L1Registry, "*"), module.PersonalConsumerHandler.Resume)
		personal.DELETE("/consumers/:id", middleware.Authorize("email:consumer:delete", module.L1Registry, "*"), module.PersonalConsumerHandler.Delete)

		personal.POST("/templates", middleware.Authorize("email:template:create", module.L1Registry, "*"), module.PersonalTemplateHandler.Create)
		personal.GET("/templates", middleware.Authorize("email:template:read", module.L1Registry, "*"), module.PersonalTemplateHandler.List)
		personal.GET("/templates/:id", middleware.Authorize("email:template:read", module.L1Registry, "*"), module.PersonalTemplateHandler.Get)
		personal.POST("/templates/:id/versions", middleware.Authorize("email:template:publish", module.L1Registry, "*"), module.PersonalTemplateHandler.PublishVersion)
		personal.GET("/templates/:id/versions", middleware.Authorize("email:template:read", module.L1Registry, "*"), module.PersonalTemplateHandler.ListVersions)
		personal.DELETE("/templates/:id", middleware.Authorize("email:template:delete", module.L1Registry, "*"), module.PersonalTemplateHandler.Delete)
	}

	tenant := router.Group("/api/v1/tenant/mail")
	{
		tenant.POST("/consumers", middleware.Authorize("email:consumer:create", module.L1Registry, "*"), module.TenantConsumerHandler.Create)
		tenant.GET("/consumers", middleware.Authorize("email:consumer:read", module.L1Registry, "*"), module.TenantConsumerHandler.List)
		tenant.GET("/consumers/:id", middleware.Authorize("email:consumer:read", module.L1Registry, "*"), module.TenantConsumerHandler.Get)
		tenant.POST("/consumers/:id/runtime/watch", middleware.Authorize("email:consumer:read", module.L1Registry, "*"), module.TenantConsumerHandler.WatchRuntime)
		tenant.PATCH("/consumers/:id", middleware.Authorize("email:consumer:update", module.L1Registry, "*"), module.TenantConsumerHandler.Update)
		tenant.POST("/consumers/:id/pause", middleware.Authorize("email:consumer:update", module.L1Registry, "*"), module.TenantConsumerHandler.Pause)
		tenant.POST("/consumers/:id/resume", middleware.Authorize("email:consumer:update", module.L1Registry, "*"), module.TenantConsumerHandler.Resume)
		tenant.DELETE("/consumers/:id", middleware.Authorize("email:consumer:delete", module.L1Registry, "*"), module.TenantConsumerHandler.Delete)

		tenant.POST("/templates", middleware.Authorize("email:template:create", module.L1Registry, "*"), module.TenantTemplateHandler.Create)
		tenant.GET("/templates", middleware.Authorize("email:template:read", module.L1Registry, "*"), module.TenantTemplateHandler.List)
		tenant.GET("/templates/:id", middleware.Authorize("email:template:read", module.L1Registry, "*"), module.TenantTemplateHandler.Get)
		tenant.POST("/templates/:id/versions", middleware.Authorize("email:template:publish", module.L1Registry, "*"), module.TenantTemplateHandler.PublishVersion)
		tenant.GET("/templates/:id/versions", middleware.Authorize("email:template:read", module.L1Registry, "*"), module.TenantTemplateHandler.ListVersions)
		tenant.DELETE("/templates/:id", middleware.Authorize("email:template:delete", module.L1Registry, "*"), module.TenantTemplateHandler.Delete)
	}
}
