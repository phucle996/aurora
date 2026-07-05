package storage

import (
	"github.com/gin-gonic/gin"
)

// [COMMENT]: RegisterRoutes thực hiện đăng ký và thiết lập định tuyến HTTP cho Storage API.
func RegisterRoutes(router *gin.Engine, module *StorageModule) {
	// ========================================================================
	// 🗄️ PHÂN KHÚC BUCKETS: QUẢN TRỊ STORAGE BUCKETS
	// ========================================================================
	
	// [COMMENT]: Tạo mới storage bucket
	router.POST("/api/v1/storage/buckets",
		module.BucketHandler.Create,
	)

	// [COMMENT]: Lấy chi tiết thông tin bucket
	router.GET("/api/v1/storage/buckets/:id",
		module.BucketHandler.Get,
	)

	// [COMMENT]: Liệt kê danh sách các buckets
	router.GET("/api/v1/storage/buckets",
		module.BucketHandler.List,
	)

	// [COMMENT]: Cập nhật hạn mức lưu trữ (Quota) của bucket
	router.PATCH("/api/v1/storage/buckets/:id/quota",
		module.BucketHandler.UpdateQuota,
	)

	// [COMMENT]: Tạm ngưng hoạt động của bucket
	router.POST("/api/v1/storage/buckets/:id/suspend",
		module.BucketHandler.Suspend,
	)

	// [COMMENT]: Kích hoạt lại bucket bị suspend
	router.POST("/api/v1/storage/buckets/:id/resume",
		module.BucketHandler.Resume,
	)

	// [COMMENT]: Yêu cầu xóa bucket
	router.DELETE("/api/v1/storage/buckets/:id",
		module.BucketHandler.Delete,
	)

	// ========================================================================
	// 🔑 PHÂN KHÚC CREDENTIALS: QUẢN LÝ ACCESS KEYS
	// ========================================================================

	// [COMMENT]: Tạo mới cặp Access Key truy cập bucket
	router.POST("/api/v1/storage/buckets/:id/credentials",
		module.CredentialHandler.Create,
	)

	// [COMMENT]: Liệt kê các Access Keys của bucket
	router.GET("/api/v1/storage/buckets/:id/credentials",
		module.CredentialHandler.List,
	)

	// [COMMENT]: Chi tiết một Access Key cụ thể
	router.GET("/api/v1/storage/credentials/:id",
		module.CredentialHandler.Get,
	)

	// [COMMENT]: Thu hồi / Xóa bỏ Access Key
	router.DELETE("/api/v1/storage/credentials/:id",
		module.CredentialHandler.Revoke,
	)
}
