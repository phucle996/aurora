package core

import (
	"controlplane/internal/http/middleware"
	"time"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(router *gin.Engine, module *Module) {
	if router == nil || module == nil || module.ZoneHandler == nil {
		return
	}

	router.POST("/admin/core/zones",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(middleware.WithInjectAccessKey()),
		middleware.AdminCriticalSignature(),
		middleware.AdminCriticalStepUp2FA(),
		module.ZoneHandler.CreateZone,
	)
	router.GET("/admin/core/zones",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.rateLimiter, "rate_limit:admin:list_zones", 50, 20, time.Minute),
		module.ZoneHandler.ListZones,
	)
	router.PATCH("/admin/core/zones/:zone_id/status",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(middleware.WithInjectAccessKey()),
		middleware.AdminCriticalSignature(),
		middleware.AdminCriticalStepUp2FA(),
		module.ZoneHandler.UpdateZoneStatus,
	)
	router.DELETE("/admin/core/zones/:zone_id",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		module.ZoneHandler.DeleteZone,
	)
	router.GET("/admin/core/zones/:zone_id/services",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		module.ZoneHandler.ListZoneServices,
	)
	router.PUT("/admin/core/zones/:zone_id/services",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		module.ZoneHandler.UpsertZoneService,
	)
}
