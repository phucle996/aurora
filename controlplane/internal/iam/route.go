package iam

import (
	"controlplane/internal/http/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(router *gin.Engine, module *IAMModule) {
	if router == nil || module == nil {
		return
	}

	router.POST("/api/v1/auth/register",
		middleware.RateLimitPostAuth(module.rateLimiter, "/api/v1/auth/register"),
		module.AuthHandler.RegisterAccount,
	)
	router.POST("/api/v1/auth/login",
		middleware.RateLimitPostAuth(module.rateLimiter, "/api/v1/auth/login"),
		module.AuthHandler.Login,
	)
	router.POST("/api/v1/auth/refresh",
		middleware.RateLimitPostAuth(module.rateLimiter, "/api/v1/auth/refresh"),
		module.RefreshTokenHandler.Refresh,
	)
	userAccessGuard := middleware.Access()

	router.GET("/api/v1/auth/session",
		userAccessGuard,
		middleware.RateLimitPostAuth(module.rateLimiter, "/api/v1/auth/session"),
		module.AuthHandler.Session,
	)
	router.POST("/api/v1/auth/logout",
		userAccessGuard,
		middleware.RateLimitPostAuth(module.rateLimiter, "/api/v1/auth/logout"),
		module.LogoutHandler.Logout,
	)
	router.GET("/api/v1/me/devices",
		userAccessGuard,
		middleware.RateLimitPostAuth(module.rateLimiter, "/api/v1/me/devices"),
		module.DeviceHandler.ListMyDevices,
	)
	router.POST("/api/v1/me/devices/:device_id/revoke",
		userAccessGuard,
		middleware.RateLimitPostAuth(module.rateLimiter, "/api/v1/me/devices/:device_id/revoke"),
		module.DeviceHandler.RevokeMyDevice,
	)
	router.POST("/api/v1/me/devices/logout-others",
		userAccessGuard,
		middleware.RateLimitPostAuth(module.rateLimiter, "/api/v1/me/devices/logout-others"),
		module.DeviceHandler.LogoutOtherDevices,
	)
	router.POST("/api/v1/me/devices/logout-all",
		userAccessGuard,
		middleware.RateLimitPostAuth(module.rateLimiter, "/api/v1/me/devices/logout-all"),
		module.DeviceHandler.LogoutAllDevices,
	)

	router.POST("/admin/auth/login",
		module.AdminAuthHandler.Login,
	)

	router.GET("/admin/auth/session",
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.rateLimiter, "/admin/auth/session"),
		module.AdminAuthHandler.Session,
	)

	router.POST("/admin/auth/logout",
		middleware.AdminAPIKeyAuth(
			middleware.WithInjectAccessKey(),
		),
		middleware.RateLimitPostAuth(module.rateLimiter, "/admin/auth/logout"),
		module.AdminAuthHandler.Logout,
	)
	router.POST("/admin/auth/refresh",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(
			middleware.WithInjectAccessKey(),
			middleware.WithInjectAccessSecret(),
		),
		middleware.RateLimitPostAuth(module.rateLimiter, "/admin/auth/refresh"),
		module.AdminAuthHandler.Refresh,
	)
	router.POST("/admin/auth/rotate-key",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(
			middleware.WithInjectAccessKey(),
		),
		middleware.RateLimitPostAuth(module.rateLimiter, "/admin/auth/rotate-key"),
		middleware.AdminCriticalSignature(),
		middleware.AdminCriticalStepUp2FA(),
		module.AdminAuthHandler.RotateKey,
	)

	router.GET("/admin/rbac/roles",
		middleware.AdminAPIKeyAuth(),
		module.RbacHandler.ListRoles,
	)
	router.POST("/admin/rbac/roles",
		middleware.AdminAPIKeyAuth(),
		module.RbacHandler.CreateRole,
	)
	router.PUT("/admin/rbac/roles/:id",
		middleware.AdminAPIKeyAuth(),
		module.RbacHandler.UpdateRole,
	)
	router.DELETE("/admin/rbac/roles/:id",
		middleware.AdminAPIKeyAuth(),
		module.RbacHandler.DeleteRole,
	)
}
