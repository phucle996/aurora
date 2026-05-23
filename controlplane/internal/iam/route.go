package iam

import (
	"time"

	"controlplane/internal/http/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(router *gin.Engine, module *Module) {
	if router == nil || module == nil {
		return
	}

	adminAuth := middleware.AdminAPIKeyAuth()

	router.POST("/api/v1/auth/register",
		middleware.RateLimit(module.rateLimiter, "iam_auth_register", 5, 5, time.Minute),
		module.AuthHandler.RegisterAccount,
	)
	router.POST("/api/v1/auth/login",
		middleware.RateLimit(module.rateLimiter, "iam_auth_login", 10, 10, time.Minute),
		module.AuthHandler.Login,
	)
	router.POST("/api/v1/auth/refresh",
		middleware.RateLimit(module.rateLimiter, "iam_auth_refresh", 10, 10, time.Minute),
		module.RefreshTokenHandler.Refresh,
	)
	router.GET("/api/v1/auth/session",
		middleware.RateLimit(module.rateLimiter, "iam_auth_session", 20, 20, time.Minute),
		middleware.Access(module.secretProvider, module.rds),
		middleware.RequireUserDeviceRuntime(module.userDeviceRuntime, 10*time.Second, middleware.UserDeviceCookieScope{Domain: module.cfg.App.PublicDomain, Path: "/"}),
		module.AuthHandler.Session,
	)
	router.POST("/api/v1/auth/logout",
		middleware.RateLimit(module.rateLimiter, "iam_auth_logout", 10, 10, time.Minute),
		middleware.Access(module.secretProvider, module.rds),
		middleware.RequireUserDeviceRuntime(module.userDeviceRuntime, 10*time.Second, middleware.UserDeviceCookieScope{Domain: module.cfg.App.PublicDomain, Path: "/"}),
		module.LogoutHandler.Logout,
	)
	userDeviceGuard := middleware.RequireUserDeviceRuntime(module.userDeviceRuntime, 10*time.Second, middleware.UserDeviceCookieScope{Domain: module.cfg.App.PublicDomain, Path: "/"})
	router.GET("/api/v1/me/devices",
		middleware.Access(module.secretProvider, module.rds),
		userDeviceGuard,
		module.DeviceHandler.ListMyDevices,
	)
	router.POST("/api/v1/me/devices/:device_id/revoke",
		middleware.Access(module.secretProvider, module.rds),
		userDeviceGuard,
		module.DeviceHandler.RevokeMyDevice,
	)
	router.POST("/api/v1/me/devices/logout-others",
		middleware.Access(module.secretProvider, module.rds),
		userDeviceGuard,
		module.DeviceHandler.LogoutOtherDevices,
	)
	router.POST("/api/v1/me/devices/logout-all",
		middleware.Access(module.secretProvider, module.rds),
		userDeviceGuard,
		module.DeviceHandler.LogoutAllDevices,
	)

	router.POST("/admin/auth/login",
		middleware.RateLimit(module.rateLimiter, "iam_admin_auth_login", 10, 10, time.Minute),
		module.AdminAuthHandler.Login,
	)

	router.GET("/admin/auth/session",
		middleware.RateLimit(module.rateLimiter, "iam_admin_auth_session", 20, 20, time.Minute),
		adminAuth,
		module.AdminAuthHandler.Session,
	)

	router.POST("/admin/auth/logout",
		middleware.RateLimit(module.rateLimiter, "iam_admin_auth_logout", 10, 10, time.Minute),
		middleware.AdminAPIKeyAuth(
			middleware.WithInjectDeviceID(),
		),
		module.AdminAuthHandler.Logout,
	)
	router.POST("/admin/auth/refresh",
		middleware.RateLimit(module.rateLimiter, "iam_admin_auth_refresh", 20, 20, time.Minute),
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(
			middleware.WithInjectDeviceID(),
			middleware.WithInjectDeviceSecret(),
		),
		middleware.AdminCriticalSignature(),
		module.AdminAuthHandler.Refresh,
	)
	router.POST("/admin/auth/rotate-key",
		middleware.RateLimit(module.rateLimiter, "iam_admin_auth_rotate_key", 5, 5, time.Minute),
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(
			middleware.WithInjectDeviceID(),
		),
		middleware.AdminCriticalSignature(),
		middleware.AdminCriticalStepUp2FA(),
		module.AdminAuthHandler.RotateKey,
	)

	router.GET("/admin/rbac/roles",
		adminAuth,
		module.RbacHandler.ListRoles,
	)
	router.POST("/admin/rbac/roles",
		adminAuth,
		module.RbacHandler.CreateRole,
	)
	router.PUT("/admin/rbac/roles/:id",
		adminAuth,
		module.RbacHandler.UpdateRole,
	)
	router.DELETE("/admin/rbac/roles/:id",
		adminAuth,
		module.RbacHandler.DeleteRole,
	)
}
