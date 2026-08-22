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
	// 🗂️ SELF IDENTITY — không phụ thuộc owner context
	// ========================================================================
	meGroup := router.Group("/api/v1/me")
	{
		meGroup.GET("/hierarchy/tenant-invitations/preview",
			module.TenantInvitationHandler.PreviewTenantInvitation,
		)
		// [COMMENT]: /me precedes /critical so ACR keeps this self route out of
		// tenant/personal path rewriting while still consuming session proof.
		meGroup.POST("/critical/hierarchy/tenant-invitations/join",
			middleware.RequireSessionProof(),
			module.TenantInvitationHandler.JoinTenantInvitation,
		)
	}

	// Owner-prefixed routes are internal rewrite targets. Browser/SDK always
	// calls the corresponding neutral route and cannot select this branch.
	personalGroup := router.Group("/api/v1/personal")
	{
		personalGroup.GET("/tenants", module.TenantHandler.ListTenants)
		personalGroup.POST("/hierarchy/workspaces",
			middleware.Authorize("hierarchy:workspace:create", module.L1Registry, "*"),
			module.WorkspacePersonalHandler.CreateWorkspacePersonal,
		)
		personalGroup.GET("/hierarchy/workspaces",
			module.WorkspacePersonalHandler.ListWorkspacesPersonal,
		)
		personalGroup.GET("/hierarchy/workspaces/catalog",
			module.WorkspacePersonalHandler.GetWorkspaceCatalogPersonal,
		)
		// Deletion is bound to the verified active workspace context. The browser
		// cannot select another resource in the URL.
		personalGroup.DELETE("/hierarchy/workspaces",
			middleware.Authorize("hierarchy:workspace:delete", module.L1Registry, "*"),
			module.WorkspacePersonalHandler.DeleteWorkspacePersonal,
		)
		// Tenant creation is a personal-owner action. A tenant session cannot
		// create a sibling tenant through a generic route.
		personalGroup.POST("/tenants",
			middleware.Authorize("hierarchy:tenant:create", module.L1Registry, "*"),
			module.TenantHandler.CreateTenant,
		)
	}

	// ========================================================================
	// 🗂️ WORKSPACE — TENANT SCOPE
	// ========================================================================
	tenantGroup := router.Group("/api/v1/tenant")
	{
		tenantGroup.POST("/critical/hierarchy/tenant-invitations",
			middleware.RequireSessionProof(),
			middleware.Authorize("hierarchy:tenant-invitation:create", module.L1Registry, "*"),
			module.TenantInvitationHandler.CreateTenantInvitation,
		)
		tenantGroup.DELETE("/critical/hierarchy/tenant-invitations/:invitation_id",
			middleware.RequireSessionProof(),
			middleware.Authorize("hierarchy:tenant-invitation:delete", module.L1Registry, "*"),
			module.TenantInvitationHandler.RevokeTenantInvitation,
		)
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
		// The authorization target and deletion target are the same ACR-injected
		// active workspace; no path parameter can select a second workspace.
		tenantGroup.DELETE("/hierarchy/workspaces",
			middleware.Authorize("hierarchy:workspace:delete", module.L1Registry, "*"),
			module.WorkspaceTenantHandler.DeleteWorkspaceTenant,
		)
	}

}
