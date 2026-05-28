package iam

import (
	"time"

	"controlplane/internal/http/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(router *gin.Engine, module *IAMModule) {
	if router == nil || module == nil {
		return
	}

	router.POST("/api/v1/auth/register",
		middleware.RateLimitPostAuth(module.rateLimiter, "iam_auth_register_rate", 30, 30, time.Minute),
		module.AuthHandler.RegisterAccount,
	)
	router.POST("/api/v1/auth/login",
		middleware.RateLimitPostAuth(module.rateLimiter, "iam_auth_login_postauth", 40, 40, time.Minute),
		module.AuthHandler.Login,
	)
	router.POST("/api/v1/auth/refresh",
		middleware.RateLimitPostAuth(module.rateLimiter, "iam_auth_refresh_postauth", 40, 40, time.Minute),
		module.RefreshTokenHandler.Refresh,
	)
	userAccessGuard := middleware.Access()

	router.GET("/api/v1/auth/session",
		userAccessGuard,
		middleware.RateLimitPostAuth(module.rateLimiter, "iam_auth_session_postauth", 40, 40, time.Minute),
		module.AuthHandler.Session,
	)
	router.POST("/api/v1/auth/logout",
		userAccessGuard,
		middleware.RateLimitPostAuth(module.rateLimiter, "iam_auth_logout_postauth", 20, 20, time.Minute),
		module.LogoutHandler.Logout,
	)
	router.GET("/api/v1/me/devices",
		userAccessGuard,
		middleware.RateLimitPostAuth(module.rateLimiter, "iam_me_devices_list_postauth", 60, 60, time.Minute),
		module.DeviceHandler.ListMyDevices,
	)
	router.POST("/api/v1/me/devices/:device_id/revoke",
		userAccessGuard,
		middleware.RateLimitPostAuth(module.rateLimiter, "iam_me_devices_revoke_postauth", 20, 20, time.Minute),
		module.DeviceHandler.RevokeMyDevice,
	)
	router.POST("/api/v1/me/devices/logout-others",
		userAccessGuard,
		middleware.RateLimitPostAuth(module.rateLimiter, "iam_me_devices_logout_others_postauth", 15, 15, time.Minute),
		module.DeviceHandler.LogoutOtherDevices,
	)
	router.POST("/api/v1/me/devices/logout-all",
		userAccessGuard,
		middleware.RateLimitPostAuth(module.rateLimiter, "iam_me_devices_logout_all_postauth", 10, 10, time.Minute),
		module.DeviceHandler.LogoutAllDevices,
	)

	router.POST("/admin/auth/login",
		module.AdminAuthHandler.Login,
	)

	router.GET("/admin/auth/session",
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.rateLimiter, "iam_admin_auth_session_postauth", 40, 40, time.Minute),
		module.AdminAuthHandler.Session,
	)

	router.POST("/admin/auth/logout",
		middleware.AdminAPIKeyAuth(
			middleware.WithInjectAccessKey(),
		),
		middleware.RateLimitPostAuth(module.rateLimiter, "iam_admin_auth_logout_postauth", 20, 20, time.Minute),
		module.AdminAuthHandler.Logout,
	)
	router.POST("/admin/auth/refresh",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(
			middleware.WithInjectAccessKey(),
			middleware.WithInjectAccessSecret(),
		),
		middleware.RateLimitPostAuth(module.rateLimiter, "iam_admin_auth_refresh_postauth", 30, 30, time.Minute),
		middleware.AdminCriticalSignature(),
		module.AdminAuthHandler.Refresh,
	)
	router.POST("/admin/auth/rotate-key",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(
			middleware.WithInjectAccessKey(),
		),
		middleware.RateLimitPostAuth(module.rateLimiter, "iam_admin_auth_rotate_key_postauth", 10, 10, time.Minute),
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
