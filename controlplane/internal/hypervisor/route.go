package hypervisor

import (
	"controlplane/internal/http/middleware"
	"controlplane/pkg/apires"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes thực hiện đăng ký toàn bộ các API endpoints cho HypervisorModule một cách tường minh,
// phẳng hóa toàn bộ URL path và không sử dụng router.Group.
func RegisterRoutes(router *gin.Engine, module *HypervisorModule) {
	if !module.IsEnabled() {
		return
	}

	// 1. Phân hệ Admin - Quản lý Image mẫu
	router.GET("/admin/hypervisor/images", module.ImageHandler.ListAdmin)
	router.POST("/admin/hypervisor/images", module.ImageHandler.RegisterMetadata)
	router.POST("/admin/hypervisor/images/:image_id/import", module.ImageHandler.BeginImport)
	router.DELETE("/admin/hypervisor/images/:image_id", module.ImageHandler.BeginDelete)

	// 2. Health & Status Probe
	router.GET("/api/v1/hypervisor/status", func(c *gin.Context) {
		apires.RespondSuccess(c, gin.H{
			"status":  "healthy",
			"message": "Phân hệ Hypervisor đang hoạt động ổn định.",
		}, "Hypervisor status normal")
	})

	// 3. Phân hệ Personal - Image Catalog
	router.GET(
		"/api/v1/personal/hypervisor/images/catalog",
		middleware.Authorize("hypervisor:image:read", module.L1Registry, "*"),
		module.ImageHandler.ListCatalog,
	)

	// 4. Phân hệ Personal - Quản lý máy ảo (VM)
	router.POST(
		"/api/v1/personal/critical/hypervisor/vms",
		middleware.RequireSessionProof(),
		middleware.Authorize("hypervisor:vm:create", module.L1Registry, "*"),
		module.VMHandler.Create,
	)
	router.GET(
		"/api/v1/personal/hypervisor/vms",
		middleware.Authorize("hypervisor:vm:read", module.L1Registry, "*"),
		module.VMHandler.List,
	)
	router.GET(
		"/api/v1/personal/hypervisor/vms/:id",
		middleware.Authorize("hypervisor:vm:read", module.L1Registry, "*"),
		module.VMHandler.Get,
	)
	router.DELETE(
		"/api/v1/personal/critical/hypervisor/vms/:id",
		middleware.RequireSessionProof(),
		middleware.Authorize("hypervisor:vm:delete", module.L1Registry, "*"),
		module.VMHandler.Delete,
	)
}
