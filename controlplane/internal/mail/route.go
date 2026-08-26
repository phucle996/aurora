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

	// [COMMENT]: `email` là RBAC capability; `mail` trong URL chỉ là transport namespace tương thích.
	router.POST("/api/v1/personal/critical/mail/consumers", middleware.RequireSessionProof(), middleware.Authorize("email:consumer:create", module.L1Registry, "*"), module.PersonalConsumerHandler.Create)
	router.GET("/api/v1/personal/mail/consumers", middleware.Authorize("email:consumer:read", module.L1Registry, "*"), module.PersonalConsumerHandler.List)
	router.GET("/api/v1/personal/mail/consumers/:id", middleware.Authorize("email:consumer:read", module.L1Registry, "*"), module.PersonalConsumerHandler.Get)
	router.POST("/api/v1/personal/mail/consumers/:id/runtime/watch", middleware.Authorize("email:consumer:read", module.L1Registry, "*"), module.PersonalConsumerHandler.WatchRuntime)
	router.PATCH("/api/v1/personal/critical/mail/consumers/:id", middleware.RequireSessionProof(), middleware.Authorize("email:consumer:update", module.L1Registry, "*"), module.PersonalConsumerHandler.Update)
	router.POST("/api/v1/personal/critical/mail/consumers/:id/pause", middleware.RequireSessionProof(), middleware.Authorize("email:consumer:update", module.L1Registry, "*"), module.PersonalConsumerHandler.Pause)
	router.POST("/api/v1/personal/critical/mail/consumers/:id/resume", middleware.RequireSessionProof(), middleware.Authorize("email:consumer:update", module.L1Registry, "*"), module.PersonalConsumerHandler.Resume)
	router.DELETE("/api/v1/personal/critical/mail/consumers/:id", middleware.RequireSessionProof(), middleware.Authorize("email:consumer:delete", module.L1Registry, "*"), module.PersonalConsumerHandler.Delete)

	router.POST("/api/v1/personal/critical/mail/templates", middleware.RequireSessionProof(), middleware.Authorize("email:template:create", module.L1Registry, "*"), module.PersonalTemplateHandler.Create)
	router.GET("/api/v1/personal/mail/templates", middleware.Authorize("email:template:read", module.L1Registry, "*"), module.PersonalTemplateHandler.List)
	router.GET("/api/v1/personal/mail/templates/:id", middleware.Authorize("email:template:read", module.L1Registry, "*"), module.PersonalTemplateHandler.Get)
	router.POST("/api/v1/personal/critical/mail/templates/:id/versions", middleware.RequireSessionProof(), middleware.Authorize("email:template:publish", module.L1Registry, "*"), module.PersonalTemplateHandler.PublishVersion)
	router.GET("/api/v1/personal/mail/templates/:id/versions", middleware.Authorize("email:template:read", module.L1Registry, "*"), module.PersonalTemplateHandler.ListVersions)
	router.DELETE("/api/v1/personal/critical/mail/templates/:id", middleware.RequireSessionProof(), middleware.Authorize("email:template:delete", module.L1Registry, "*"), module.PersonalTemplateHandler.Delete)

	router.POST("/api/v1/tenant/critical/mail/consumers", middleware.RequireSessionProof(), middleware.Authorize("email:consumer:create", module.L1Registry, "*"), module.TenantConsumerHandler.Create)
	router.GET("/api/v1/tenant/mail/consumers", middleware.Authorize("email:consumer:read", module.L1Registry, "*"), module.TenantConsumerHandler.List)
	router.GET("/api/v1/tenant/mail/consumers/:id", middleware.Authorize("email:consumer:read", module.L1Registry, "*"), module.TenantConsumerHandler.Get)
	router.POST("/api/v1/tenant/mail/consumers/:id/runtime/watch", middleware.Authorize("email:consumer:read", module.L1Registry, "*"), module.TenantConsumerHandler.WatchRuntime)
	router.PATCH("/api/v1/tenant/critical/mail/consumers/:id", middleware.RequireSessionProof(), middleware.Authorize("email:consumer:update", module.L1Registry, "*"), module.TenantConsumerHandler.Update)
	router.POST("/api/v1/tenant/critical/mail/consumers/:id/pause", middleware.RequireSessionProof(), middleware.Authorize("email:consumer:update", module.L1Registry, "*"), module.TenantConsumerHandler.Pause)
	router.POST("/api/v1/tenant/critical/mail/consumers/:id/resume", middleware.RequireSessionProof(), middleware.Authorize("email:consumer:update", module.L1Registry, "*"), module.TenantConsumerHandler.Resume)
	router.DELETE("/api/v1/tenant/critical/mail/consumers/:id", middleware.RequireSessionProof(), middleware.Authorize("email:consumer:delete", module.L1Registry, "*"), module.TenantConsumerHandler.Delete)

	router.POST("/api/v1/tenant/critical/mail/templates", middleware.RequireSessionProof(), middleware.Authorize("email:template:create", module.L1Registry, "*"), module.TenantTemplateHandler.Create)
	router.GET("/api/v1/tenant/mail/templates", middleware.Authorize("email:template:read", module.L1Registry, "*"), module.TenantTemplateHandler.List)
	router.GET("/api/v1/tenant/mail/templates/:id", middleware.Authorize("email:template:read", module.L1Registry, "*"), module.TenantTemplateHandler.Get)
	router.POST("/api/v1/tenant/critical/mail/templates/:id/versions", middleware.RequireSessionProof(), middleware.Authorize("email:template:publish", module.L1Registry, "*"), module.TenantTemplateHandler.PublishVersion)
	router.GET("/api/v1/tenant/mail/templates/:id/versions", middleware.Authorize("email:template:read", module.L1Registry, "*"), module.TenantTemplateHandler.ListVersions)
	router.DELETE("/api/v1/tenant/critical/mail/templates/:id", middleware.RequireSessionProof(), middleware.Authorize("email:template:delete", module.L1Registry, "*"), module.TenantTemplateHandler.Delete)
}
