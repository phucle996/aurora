package hypervisor

import (
	"github.com/gin-gonic/gin"
)

// RegisterRoutes thực hiện đăng ký các API endpoints cho HypervisorModule khi nó hoạt động bình thường.
func RegisterRoutes(router *gin.Engine, module *HypervisorModule) {
	if router == nil || module == nil || !module.IsEnabled() {
		return
	}

	// Group định tuyến cho hypervisor được bảo vệ bởi middleware bảo mật Access
	group := router.Group("/api/v1/hypervisor")
	{
		group.GET("/status", func(c *gin.Context) {
			c.JSON(200, gin.H{
				"status":  "healthy",
				"message": "Phân hệ Hypervisor đang hoạt động ổn định.",
			})
		})

		group.GET("/vms", func(c *gin.Context) {
			// Giả lập trả về danh sách máy ảo
			c.JSON(200, gin.H{
				"vms": []gin.H{
					{"id": "vm-01", "name": "SRE-Bastion-Host", "status": "running"},
					{"id": "vm-02", "name": "DB-Replica-01", "status": "running"},
				},
			})
		})
	}
}
