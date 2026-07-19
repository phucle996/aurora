package mail

import "github.com/gin-gonic/gin"

// RegisterRoutes nhận path đã được Envoy/ACR rewrite sang đúng nhánh Personal hoặc Tenant,
// giống IAM. Handler không tự suy luận scope từ body và repository vẫn fail-close bằng DB scope check.
func RegisterRoutes(router *gin.Engine, module *Module) {
	if router == nil || module == nil {
		return
	}

	personal := router.Group("/api/v1/personal/mail")
	{
		personal.POST("/consumers", module.PersonalConsumerHandler.Create)
		personal.GET("/consumers", module.PersonalConsumerHandler.List)
		personal.GET("/consumers/:id", module.PersonalConsumerHandler.Get)
		personal.PATCH("/consumers/:id", module.PersonalConsumerHandler.Update)
		personal.POST("/consumers/:id/pause", module.PersonalConsumerHandler.Pause)
		personal.POST("/consumers/:id/resume", module.PersonalConsumerHandler.Resume)
		personal.DELETE("/consumers/:id", module.PersonalConsumerHandler.Delete)

		personal.POST("/templates", module.PersonalTemplateHandler.Create)
		personal.GET("/templates", module.PersonalTemplateHandler.List)
		personal.GET("/templates/:id", module.PersonalTemplateHandler.Get)
		personal.POST("/templates/:id/versions", module.PersonalTemplateHandler.PublishVersion)
		personal.GET("/templates/:id/versions", module.PersonalTemplateHandler.ListVersions)
		personal.POST("/templates/:id/archive", module.PersonalTemplateHandler.Archive)
	}

	tenant := router.Group("/api/v1/tenant/mail")
	{
		tenant.POST("/consumers", module.TenantConsumerHandler.Create)
		tenant.GET("/consumers", module.TenantConsumerHandler.List)
		tenant.GET("/consumers/:id", module.TenantConsumerHandler.Get)
		tenant.PATCH("/consumers/:id", module.TenantConsumerHandler.Update)
		tenant.POST("/consumers/:id/pause", module.TenantConsumerHandler.Pause)
		tenant.POST("/consumers/:id/resume", module.TenantConsumerHandler.Resume)
		tenant.DELETE("/consumers/:id", module.TenantConsumerHandler.Delete)

		tenant.POST("/templates", module.TenantTemplateHandler.Create)
		tenant.GET("/templates", module.TenantTemplateHandler.List)
		tenant.GET("/templates/:id", module.TenantTemplateHandler.Get)
		tenant.POST("/templates/:id/versions", module.TenantTemplateHandler.PublishVersion)
		tenant.GET("/templates/:id/versions", module.TenantTemplateHandler.ListVersions)
		tenant.POST("/templates/:id/archive", module.TenantTemplateHandler.Archive)
	}
}
