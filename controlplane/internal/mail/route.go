package mail

import (
	"github.com/gin-gonic/gin"
)

// RegisterRoutes thực hiện đăng ký và thiết lập chuỗi phòng ngự (Security Chain) cho toàn bộ Mail API.
// [COMMENT]: Đã gỡ bỏ hoàn toàn RateLimitPostAuth middleware tại đây.
// Toàn bộ logic giới hạn tần suất (Rate Limiting) hiện đã được bàn giao (offload)
// lên tầng Rust ACL (Edge) chạy trước Envoy để tăng tính HA và giảm tải cho Control Plane.
func RegisterRoutes(router *gin.Engine, module *Module) {
	if router == nil || module == nil {
		return
	}

	// 1) Consumer Management (REST APIs under /api/v1/mail/consumers)
	router.POST("/api/v1/mail/consumers",
		module.ConsumerHandler.Create,
	)
	router.GET("/api/v1/mail/consumers",
		module.ConsumerHandler.List,
	)
	router.GET("/api/v1/mail/consumers/:id",
		module.ConsumerHandler.Get,
	)
	router.PATCH("/api/v1/mail/consumers/:id",
		module.ConsumerHandler.Update,
	)
	router.DELETE("/api/v1/mail/consumers/:id",
		module.ConsumerHandler.Delete,
	)
	router.PATCH("/api/v1/mail/consumers/:id/status",
		module.ConsumerHandler.UpdateStatus,
	)
	router.POST("/api/v1/mail/consumers/:id/test-connect",
		module.ConsumerHandler.TestConnection,
	)

	// 2) Template Management (REST APIs under /api/v1/mail/templates)
	router.POST("/api/v1/mail/templates",
		module.TemplateHandler.Create,
	)
	router.GET("/api/v1/mail/templates",
		module.TemplateHandler.List,
	)
	router.GET("/api/v1/mail/templates/:id",
		module.TemplateHandler.Get,
	)
	router.PATCH("/api/v1/mail/templates/:id",
		module.TemplateHandler.Update,
	)
	router.DELETE("/api/v1/mail/templates/:id",
		module.TemplateHandler.Delete,
	)
}
