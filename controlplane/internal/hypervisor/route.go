package hypervisor

import (
	"controlplane/internal/http/middleware"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes thực hiện đăng ký các API endpoints cho HypervisorModule khi nó hoạt động bình thường.
func RegisterRoutes(router *gin.Engine, module *HypervisorModule) {
	if !module.IsEnabled() {
		return
	}

	adminImages := router.Group("/admin/hypervisor/images")
	adminImages.GET("", module.ImageHandler.ListAdmin)
	adminImages.POST("", module.ImageHandler.RegisterMetadata)
	adminImages.POST("/:image_id/import", module.ImageHandler.BeginImport)
	adminImages.DELETE("/:image_id", module.ImageHandler.BeginDelete)

	statusGroup := router.Group("/api/v1/hypervisor")
	{
		statusGroup.GET("/status", func(c *gin.Context) {
			c.JSON(200, gin.H{
				"status":  "healthy",
				"message": "Phân hệ Hypervisor đang hoạt động ổn định.",
			})
		})
	}

	personal := router.Group("/api/v1/personal/hypervisor")
	personal.GET(
		"/images/catalog",
		middleware.Authorize("hypervisor:image:read", module.L1Registry, "*"),
		module.ImageHandler.ListCatalog,
	)
	personal.POST(
		"/vms",
		middleware.Authorize("hypervisor:vm:create", module.L1Registry, "*"),
		module.VMHandler.Create,
	)
	personal.GET(
		"/vms",
		middleware.Authorize("hypervisor:vm:read", module.L1Registry, "*"),
		module.VMHandler.List,
	)
	personal.GET(
		"/vms/:id",
		middleware.Authorize("hypervisor:vm:read", module.L1Registry, "*"),
		module.VMHandler.Get,
	)
	personal.DELETE(
		"/vms/:id",
		middleware.Authorize("hypervisor:vm:delete", module.L1Registry, "*"),
		module.VMHandler.Delete,
	)
}
