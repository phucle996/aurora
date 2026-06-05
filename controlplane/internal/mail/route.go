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
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/consumers"),
		module.ConsumerHandler.Create,
	)
	router.GET("/api/v1/mail/consumers",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/consumers"),
		module.ConsumerHandler.List,
	)
	router.GET("/api/v1/mail/consumers/:id",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/consumers/:id"),
		module.ConsumerHandler.Get,
	)
	router.PATCH("/api/v1/mail/consumers/:id",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/consumers/:id"),
		module.ConsumerHandler.Update,
	)
	router.DELETE("/api/v1/mail/consumers/:id",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/consumers/:id"),
		module.ConsumerHandler.Delete,
	)
	router.PATCH("/api/v1/mail/consumers/:id/status",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/consumers/:id/status"),
		module.ConsumerHandler.UpdateStatus,
	)
	router.POST("/api/v1/mail/consumers/:id/test-connect",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/consumers/:id/test-connect"),
		module.ConsumerHandler.TestConnection,
	)

	// 2) Template Management (REST APIs under /api/v1/mail/templates)
	router.POST("/api/v1/mail/templates",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/templates"),
		module.TemplateHandler.Create,
	)
	router.GET("/api/v1/mail/templates",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/templates"),
		module.TemplateHandler.List,
	)
	router.GET("/api/v1/mail/templates/:id",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/templates/:id"),
		module.TemplateHandler.Get,
	)
	router.PATCH("/api/v1/mail/templates/:id",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/templates/:id"),
		module.TemplateHandler.Update,
	)
	router.DELETE("/api/v1/mail/templates/:id",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/templates/:id"),
		module.TemplateHandler.Delete,
	)

	// 3) Gateway Management (REST APIs under /api/v1/mail/gateways)
	router.POST("/api/v1/mail/gateways",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/gateways"),
		module.GatewayHandler.Create,
	)
	router.GET("/api/v1/mail/gateways",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/gateways"),
		module.GatewayHandler.List,
	)
	router.GET("/api/v1/mail/gateways/:id",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/gateways/:id"),
		module.GatewayHandler.Get,
	)
	router.PATCH("/api/v1/mail/gateways/:id",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/gateways/:id"),
		module.GatewayHandler.Update,
	)
	router.DELETE("/api/v1/mail/gateways/:id",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "/api/v1/mail/gateways/:id"),
		module.GatewayHandler.Delete,
	)

	// 4) Endpoint Management (Infrastructural REST APIs under /admin/mail/endpoints)
	router.POST("/admin/mail/endpoints",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "/admin/mail/endpoints"),
		module.EndpointHandler.Create,
	)
	router.GET("/admin/mail/endpoints",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "/admin/mail/endpoints"),
		module.EndpointHandler.List,
	)

	router.GET("/admin/mail/endpoints/:id",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "/admin/mail/endpoints/:id"),
		module.EndpointHandler.Get,
	)
	router.PATCH("/admin/mail/endpoints/:id",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "/admin/mail/endpoints/:id"),
		module.EndpointHandler.Update,
	)
	router.DELETE("/admin/mail/endpoints/:id",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "/admin/mail/endpoints/:id"),
		module.EndpointHandler.Delete,
	)
	router.POST("/admin/mail/endpoints/:id/test-connect",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "/admin/mail/endpoints/:id/test-connect"),
		module.EndpointHandler.TestConnection,
	)
	router.POST("/admin/mail/endpoints/try-connect",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "/admin/mail/endpoints/try-connect"),
		module.EndpointHandler.TestConnectionRaw,
	)
}
