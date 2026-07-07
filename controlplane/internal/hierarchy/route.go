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
	// 🗂️ WORKSPACE — CUSTOMER WORKSPACE LIFECYCLE MANAGEMENT
	// ========================================================================

	// [COMMENT]: Tạo workspace mới — yêu cầu quyền hierarchy:workspace:create ở tầng *
	router.POST("/api/v1/workspaces",
		middleware.Authorize("hierarchy:workspace:create", module.L1Registry, "*"),
		module.WorkspaceHandler.CreateWorkspace,
	)
	// [COMMENT]: Lấy danh sách workspace mà user có quyền read (phân giải qua L1 cache của role)
	router.GET("/api/v1/workspaces",
		middleware.Authorize("hierarchy:workspace:read", module.L1Registry, "*"),
		module.WorkspaceHandler.ListWorkspaces,
	)
	// [COMMENT]: Hot path catalog — trả về id,code,name tối giản lọc theo zone + tenant/personal context
	router.GET("/api/v1/workspaces/catalog",
		middleware.Authorize("hierarchy:workspace:read", module.L1Registry, "*"),
		module.WorkspaceHandler.GetWorkspaceCatalog,
	)

	// [COMMENT]: Route tạo tenant mới — cần x-user-id header bắt buộc, x-tenant-id phải trống
	router.POST("/api/v1/tenants",
		module.TenantHandler.CreateTenant,
	)
}

