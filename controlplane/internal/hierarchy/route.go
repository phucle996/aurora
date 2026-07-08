package core

import (
	"controlplane/internal/http/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(router *gin.Engine, module *Module) {
	// [COMMENT]: Route tạo zone yêu cầu OTP + chữ ký Ed25519 (chứa /critical/ để ACR chặn bắt song song)
	router.POST("/admin/critical/core/zones",
		module.ZoneHandler.CreateZone,
	)
	router.GET("/admin/core/zones",
		module.ZoneHandler.ListZones,
	)
	router.GET("/admin/core/zones/:zone_id",
		module.ZoneHandler.GetDetailZone,
	)

	// [COMMENT]: Route cập nhật trạng thái zone yêu cầu OTP + chữ ký Ed25519 (chứa /critical/ để ACR chặn bắt song song)
	router.PATCH("/admin/critical/core/zones/:zone_id/status",
		module.ZoneHandler.UpdateZoneStatus,
	)

	router.DELETE("/admin/critical/core/zones/:zone_id",
		module.ZoneHandler.DeleteZone,
	)
	// router.GET("/admin/core/zones/:zone_id/services",
	// 	module.ZoneHandler.ListZoneServices,
	// )
	router.PUT("/admin/critical/core/zones/services",
		module.ZoneHandler.UpdateZoneService,
	)

	// ========================================================================
	// 🗂️ WORKSPACE — PLATFORM/PERSONAL SCOPE
	// ========================================================================
	platformGroup := router.Group("/api/v1/platform")
	{
		// [COMMENT]: Tạo workspace cá nhân mới — yêu cầu quyền hierarchy:workspace:create ở tầng *
		platformGroup.POST("/hierarchy/workspaces",
			middleware.Authorize("hierarchy:workspace:create", module.L1Registry, "*"),
			module.WorkspacePlatformHandler.CreateWorkspacePlatform,
		)
		// [COMMENT]: Lấy danh sách workspace cá nhân mà user có quyền read
		platformGroup.GET("/hierarchy/workspaces",
			middleware.Authorize("hierarchy:workspace:read", module.L1Registry, "*"),
			module.WorkspacePlatformHandler.ListWorkspacesPlatform,
		)
		// [COMMENT]: Hot path catalog cá nhân — không dùng Authorize do tình huống chicken-and-egg
		platformGroup.GET("/hierarchy/workspaces/catalog",
			module.WorkspacePlatformHandler.GetWorkspaceCatalogPlatform,
		)
	}

	// ========================================================================
	// 🗂️ WORKSPACE — TENANT SCOPE
	// ========================================================================
	tenantGroup := router.Group("/api/v1/tenant")
	{
		// [COMMENT]: Tạo workspace thuộc tenant — yêu cầu quyền hierarchy:workspace:create ở tầng *
		tenantGroup.POST("/hierarchy/workspaces",
			middleware.Authorize("hierarchy:workspace:create", module.L1Registry, "*"),
			module.WorkspaceTenantHandler.CreateWorkspaceTenant,
		)
		// [COMMENT]: Lấy danh sách workspace thuộc tenant mà user có quyền read
		tenantGroup.GET("/hierarchy/workspaces",
			middleware.Authorize("hierarchy:workspace:read", module.L1Registry, "*"),
			module.WorkspaceTenantHandler.ListWorkspacesTenant,
		)
		// [COMMENT]: Hot path catalog của tenant — không dùng Authorize do tình huống chicken-and-egg
		tenantGroup.GET("/hierarchy/workspaces/catalog",
			module.WorkspaceTenantHandler.GetWorkspaceCatalogTenant,
		)
	}

	// [COMMENT]: Route tạo tenant mới — cần x-user-id header bắt buộc, x-tenant-id phải trống
	router.POST("/api/v1/tenants",
		module.TenantHandler.CreateTenant,
	)
}
