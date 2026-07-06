package storage

import (
	"controlplane/internal/http/middleware"

	"github.com/gin-gonic/gin"
)

// [COMMENT]: RegisterRoutes thực hiện đăng ký và thiết lập định tuyến HTTP cho Storage API.
func RegisterRoutes(router *gin.Engine, module *StorageModule) {
	// ========================================================================
	// 🗄️ PHÂN KHÚC BUCKETS: QUẢN TRỊ STORAGE BUCKETS
	// ========================================================================
	
	// [COMMENT]: Tạo mới storage bucket
	router.POST("/api/v1/storage/buckets",
		middleware.Authorize("storage:bucket:create", module.L1Registry, "*"),
		module.BucketHandler.Create,
	)

	// [COMMENT]: Lấy chi tiết thông tin bucket
	router.GET("/api/v1/storage/buckets/:id",
		middleware.Authorize("storage:bucket:read", module.L1Registry, "*"),
		module.BucketHandler.Get,
	)

	// [COMMENT]: Liệt kê danh sách các buckets
	router.GET("/api/v1/storage/buckets",
		middleware.Authorize("storage:bucket:read", module.L1Registry, "*"),
		module.BucketHandler.List,
	)

	// [COMMENT]: Cập nhật hạn mức lưu trữ (Quota) của bucket
	router.PATCH("/api/v1/storage/buckets/:id/quota",
		middleware.Authorize("storage:bucket:update", module.L1Registry, "*"),
		module.BucketHandler.UpdateQuota,
	)

	// [COMMENT]: Tạm ngưng hoạt động của bucket
	router.POST("/api/v1/storage/buckets/:id/suspend",
		middleware.Authorize("storage:bucket:update", module.L1Registry, "*"),
		module.BucketHandler.Suspend,
	)

	// [COMMENT]: Kích hoạt lại bucket bị suspend
	router.POST("/api/v1/storage/buckets/:id/resume",
		middleware.Authorize("storage:bucket:update", module.L1Registry, "*"),
		module.BucketHandler.Resume,
	)

	// [COMMENT]: Yêu cầu xóa bucket
	router.DELETE("/api/v1/storage/buckets/:id",
		middleware.Authorize("storage:bucket:delete", module.L1Registry, "*"),
		module.BucketHandler.Delete,
	)

	// ========================================================================
	// 🔑 PHÂN KHÚC CREDENTIALS: QUẢN LÝ ACCESS KEYS
	// ========================================================================

	// [COMMENT]: Tạo mới cặp Access Key truy cập bucket
	router.POST("/api/v1/storage/buckets/:id/credentials",
		middleware.Authorize("storage:credential:create", module.L1Registry, "*"),
		module.CredentialHandler.Create,
	)

	// [COMMENT]: Liệt kê các Access Keys của bucket
	router.GET("/api/v1/storage/buckets/:id/credentials",
		middleware.Authorize("storage:credential:read", module.L1Registry, "*"),
		module.CredentialHandler.List,
	)

	// [COMMENT]: Chi tiết một Access Key cụ thể
	router.GET("/api/v1/storage/credentials/:id",
		middleware.Authorize("storage:credential:read", module.L1Registry, "*"),
		module.CredentialHandler.Get,
	)

	// [COMMENT]: Thu hồi / Xóa bỏ Access Key
	router.DELETE("/api/v1/storage/credentials/:id",
		middleware.Authorize("storage:credential:delete", module.L1Registry, "*"),
		module.CredentialHandler.Revoke,
	)
}
