package mail

import (
	"controlplane/internal/http/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(router *gin.Engine, module *Module) {
	if router == nil || module == nil {
		return
	}

	// 1) Consumer Management (REST APIs under /api/v1/mail/consumers)
	router.POST("/api/v1/mail/consumers",
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/consumers"),
		module.ConsumerHandler.Create,
	)
	router.GET("/api/v1/mail/consumers",
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/consumers"),
		module.ConsumerHandler.List,
	)
	router.GET("/api/v1/mail/consumers/:id",
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/consumers/:id"),
		module.ConsumerHandler.Get,
	)
	router.PATCH("/api/v1/mail/consumers/:id",
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/consumers/:id"),
		module.ConsumerHandler.Update,
	)
	router.DELETE("/api/v1/mail/consumers/:id",
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/consumers/:id"),
		module.ConsumerHandler.Delete,
	)
	router.PATCH("/api/v1/mail/consumers/:id/status",
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/consumers/:id/status"),
		module.ConsumerHandler.UpdateStatus,
	)
	router.POST("/api/v1/mail/consumers/:id/test-connect",
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/consumers/:id/test-connect"),
		module.ConsumerHandler.TestConnection,
	)

	// 2) Template Management (REST APIs under /api/v1/mail/templates)
	router.POST("/api/v1/mail/templates",
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/templates"),
		module.TemplateHandler.Create,
	)
	router.GET("/api/v1/mail/templates",
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/templates"),
		module.TemplateHandler.List,
	)
	router.GET("/api/v1/mail/templates/:id",
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/templates/:id"),
		module.TemplateHandler.Get,
	)
	router.PATCH("/api/v1/mail/templates/:id",
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/templates/:id"),
		module.TemplateHandler.Update,
	)
	router.DELETE("/api/v1/mail/templates/:id",
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/templates/:id"),
		module.TemplateHandler.Delete,
	)
}
