package core

import (
	"github.com/gin-gonic/gin"
)

func RegisterRoutes(router *gin.Engine, module *Module) {
	// [COMMENT]: Route tạo zone yêu cầu OTP + chữ ký Ed25519 (chứa /critical/ để ACL chặn bắt song song)
	router.POST("/admin/critical/core/zones",
		module.ZoneHandler.CreateZone,
	)
	router.GET("/admin/core/zones",
		module.ZoneHandler.ListZones,
	)
	router.GET("/admin/core/zones/:zone_id",
		module.ZoneHandler.GetZone,
	)

	// [COMMENT]: Route cập nhật trạng thái zone yêu cầu OTP + chữ ký Ed25519 (chứa /critical/ để ACL chặn bắt song song)
	router.PATCH("/admin/critical/core/zones/status",
		module.ZoneHandler.UpdateZoneStatus,
	)

	router.DELETE("/admin/core/zones/:zone_id",
		module.ZoneHandler.DeleteZone,
	)
	router.GET("/admin/core/zones/:zone_id/services",
		module.ZoneHandler.ListZoneServices,
	)
	router.PUT("/admin/core/zones/services",
		module.ZoneHandler.UpsertZoneService,
	)
}
