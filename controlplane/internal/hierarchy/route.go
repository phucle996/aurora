package hierarchy

import (
	"controlplane/internal/http/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(router *gin.Engine, module *Module) {
	// [COMMENT]: Route tạo zone yêu cầu OTP + chữ ký Ed25519 (chứa /critical/ để ACR chặn bắt song song)
	router.POST("/admin/critical/hierarchy/zones",
		module.ZoneHandler.CreateZone,
	)
	router.GET("/admin/hierarchy/zones",
		module.ZoneHandler.ListZones,
	)
	router.GET("/admin/hierarchy/zones/:zone_id",
		module.ZoneHandler.GetDetailZone,
	)
	router.GET("/admin/hierarchy/zones/:zone_id/encryption-keys",
		module.ZoneEncryptionKeyHandler.ListZoneEncryptionKeys,
	)

	// [COMMENT]: Public-key lifecycle controls which key protects future Zone
	// commands, so every mutation is structurally bound to ACR critical proof.
	router.POST("/admin/critical/hierarchy/zones/:zone_id/encryption-keys",
		module.ZoneEncryptionKeyHandler.RegisterZoneEncryptionKey,
	)
	router.POST("/admin/critical/hierarchy/zones/:zone_id/encryption-keys/:key_id/activate",
		module.ZoneEncryptionKeyHandler.ActivateZoneEncryptionKey,
	)
	router.POST("/admin/critical/hierarchy/zones/:zone_id/encryption-keys/:key_id/retire",
		module.ZoneEncryptionKeyHandler.RetireZoneEncryptionKey,
	)

	// [COMMENT]: Route cập nhật trạng thái zone yêu cầu OTP + chữ ký Ed25519 (chứa /critical/ để ACR chặn bắt song song)
	router.PATCH("/admin/critical/hierarchy/zones/:zone_id/status",
		module.ZoneHandler.UpdateZoneStatus,
	)

	router.DELETE("/admin/critical/hierarchy/zones/:zone_id",
		module.ZoneHandler.DeleteZone,
	)
	router.PUT("/admin/critical/hierarchy/zones/services",
		module.ZoneHandler.UpdateZoneService,
	)

	// ========================================================================
	// 🗂️ WORKSPACE — PERSONAL SCOPE (/api/v1/me)
	// [COMMENT]: Nhóm /me phục vụ các hành động tự-phục-vụ của người dùng hiện tại
	// tương tự pattern /api/v1/me trong IAM route
	// ========================================================================
	meGroup := router.Group("/api/v1/me")
	{
		// [COMMENT]: Tạo workspace cá nhân mới — vẫn cần quyền hierarchy:workspace:create
		meGroup.POST("/hierarchy/workspace/create",
			middleware.Authorize("hierarchy:workspace:create", module.L1Registry, "*"),
			module.WorkspacePersonalHandler.CreateWorkspacePersonal,
		)
		// [COMMENT]: Lấy danh sách workspace cá nhân mà user có quyền read
		meGroup.GET("/hierarchy/workspace/read",
			module.WorkspacePersonalHandler.ListWorkspacesPersonal,
		)
		// [COMMENT]: Hot path catalog cá nhân — không dùng Authorize do tình huống chicken-and-egg
		meGroup.GET("/hierarchy/workspace/catalog",
			module.WorkspacePersonalHandler.GetWorkspaceCatalogPersonal,
		)
		// [COMMENT]: Xóa workspace cá nhân — không áp Authorize middleware,
		// ownership được kiểm tra trong handler qua X-User-ID header
		meGroup.DELETE("/hierarchy/workspace/delete/:workspace_id",
			module.WorkspacePersonalHandler.DeleteWorkspacePersonal,
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
		// [COMMENT]: Xóa workspace thuộc tenant — yêu cầu quyền hierarchy:workspace:delete
		tenantGroup.DELETE("/hierarchy/workspaces/:workspace_id",
			middleware.Authorize("hierarchy:workspace:delete", module.L1Registry, "*"),
			module.WorkspaceTenantHandler.DeleteWorkspaceTenant,
		)
	}

	// [COMMENT]: Route tạo tenant mới — cần x-user-id header bắt buộc, x-tenant-id phải trống
	router.POST("/api/v1/tenants",
		module.TenantHandler.CreateTenant,
	)
}
