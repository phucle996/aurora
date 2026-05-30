package mail

import (
	"time"

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
		middleware.RateLimitPostAuth(module.RateLimiter, "rate_limit:mail:consumers_create", 10, 10, time.Minute),
		module.ConsumerHandler.Create,
	)
	router.GET("/api/v1/mail/consumers",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "rate_limit:mail:consumers_list", 50, 50, time.Minute),
		module.ConsumerHandler.List,
	)
	router.GET("/api/v1/mail/consumers/:id",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "rate_limit:mail:consumers_get", 50, 50, time.Minute),
		module.ConsumerHandler.Get,
	)
	router.PATCH("/api/v1/mail/consumers/:id",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "rate_limit:mail:consumers_update", 20, 20, time.Minute),
		module.ConsumerHandler.Update,
	)
	router.DELETE("/api/v1/mail/consumers/:id",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "rate_limit:mail:consumers_delete", 10, 10, time.Minute),
		module.ConsumerHandler.Delete,
	)
	router.PATCH("/api/v1/mail/consumers/:id/status",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "rate_limit:mail:consumers_status", 20, 20, time.Minute),
		module.ConsumerHandler.UpdateStatus,
	)
	router.POST("/api/v1/mail/consumers/:id/test-connect",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "rate_limit:mail:consumers_test_connect", 5, 5, time.Minute),
		module.ConsumerHandler.TestConnection,
	)

	// 2) Template Management (REST APIs under /api/v1/mail/templates)
	router.POST("/api/v1/mail/templates",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "rate_limit:mail:templates_create", 15, 15, time.Minute),
		module.TemplateHandler.Create,
	)
	router.GET("/api/v1/mail/templates",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "rate_limit:mail:templates_list", 60, 60, time.Minute),
		module.TemplateHandler.List,
	)
	router.GET("/api/v1/mail/templates/:id",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "rate_limit:mail:templates_get", 60, 60, time.Minute),
		module.TemplateHandler.Get,
	)
	router.PATCH("/api/v1/mail/templates/:id",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "rate_limit:mail:templates_update", 30, 30, time.Minute),
		module.TemplateHandler.Update,
	)
	router.DELETE("/api/v1/mail/templates/:id",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "rate_limit:mail:templates_delete", 15, 15, time.Minute),
		module.TemplateHandler.Delete,
	)

	// 3) Gateway Management (REST APIs under /api/v1/mail/gateways)
	router.POST("/api/v1/mail/gateways",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "rate_limit:mail:gateways_create", 10, 10, time.Minute),
		module.GatewayHandler.Create,
	)
	router.GET("/api/v1/mail/gateways",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "rate_limit:mail:gateways_list", 40, 40, time.Minute),
		module.GatewayHandler.List,
	)
	router.GET("/api/v1/mail/gateways/:id",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "rate_limit:mail:gateways_get", 40, 40, time.Minute),
		module.GatewayHandler.Get,
	)
	router.PATCH("/api/v1/mail/gateways/:id",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "rate_limit:mail:gateways_update", 20, 20, time.Minute),
		module.GatewayHandler.Update,
	)
	router.DELETE("/api/v1/mail/gateways/:id",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "rate_limit:mail:gateways_delete", 10, 10, time.Minute),
		module.GatewayHandler.Delete,
	)

	// 4) Endpoint Management (Infrastructural REST APIs under /admin/mail/endpoints)
	router.POST("/admin/mail/endpoints",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "rate_limit:admin:create_endpoints", 10, 10, time.Minute),
		module.EndpointHandler.Create,
	)
	router.GET("/admin/mail/endpoints",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "rate_limit:admin:list_endpoints", 40, 40, time.Minute),
		module.EndpointHandler.List,
	)
	router.GET("/admin/mail/endpoints/:id",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "rate_limit:admin:get_endpoints", 40, 40, time.Minute),
		module.EndpointHandler.Get,
	)
	router.PATCH("/admin/mail/endpoints/:id",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "rate_limit:admin:update_endpoints", 20, 20, time.Minute),
		module.EndpointHandler.Update,
	)
	router.DELETE("/admin/mail/endpoints/:id",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.RateLimiter, "rate_limit:admin:delete_endpoints", 10, 10, time.Minute),
		module.EndpointHandler.Delete,
	)
}
