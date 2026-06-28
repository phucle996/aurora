package core

import (
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

	// [COMMENT]: Route tạo workspace mới — cần x-zone-id header bắt buộc, x-tenant-id optional
	router.POST("/api/v1/workspaces",
		module.WorkspaceHandler.CreateWorkspace,
	)
}
