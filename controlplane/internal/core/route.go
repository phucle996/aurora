package core

import (
	"controlplane/internal/http/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(router *gin.Engine, module *Module) {
	router.POST("/admin/core/zones",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(middleware.WithInjectAdminAccessKey()),
		middleware.RateLimitPostAuth(module.rateLimiter, "/admin/core/zones"),
		middleware.AdminCriticalSignature(),
		middleware.AdminCriticalStepUp2FA(),
		module.ZoneHandler.CreateZone,
	)
	// SRE HA Warning: Endpoint này được chuyển sang chế độ không cần xác thực (public)
	// để trang đăng nhập (Login Page) có thể gọi lấy danh mục các Zone trước khi admin đăng nhập.
	// Cơ chế Rate Limiter (RateLimitPostAuth) vẫn được giữ lại để chống DDoS IP.
	router.GET("/admin/core/zones/catalog",
		middleware.RateLimitPostAuth(module.rateLimiter, "/admin/core/zones/catalog"),
		module.ZoneHandler.GetZoneCatalog,
	)

	router.GET("/api/v1/zones/catalog",
		middleware.ACL(),
		middleware.RateLimitPostAuth(module.rateLimiter, "/api/v1/zones/catalog"),
		module.ZoneHandler.GetZoneCatalog,
	)

	router.GET("/admin/core/zones",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.rateLimiter, "/admin/core/zones"),
		module.ZoneHandler.ListZones,
	)
	router.GET("/admin/core/zones/:zone_id",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		module.ZoneHandler.GetZone,
	)

	router.PATCH("/admin/core/zones/status",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(middleware.WithInjectAdminAccessKey()),
		middleware.RateLimitPostAuth(module.rateLimiter, "/admin/core/zones/status"),
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
	router.PUT("/admin/core/zones/services",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(),
		module.ZoneHandler.UpsertZoneService,
	)
}
