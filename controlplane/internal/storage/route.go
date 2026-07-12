package storage

import (
	"controlplane/internal/http/middleware"

	"github.com/gin-gonic/gin"
)

// [COMMENT]: RegisterRoutes thực hiện đăng ký và thiết lập định tuyến HTTP cho Storage API.
// Tách nhóm định tuyến thành personal và tenant tương tự như IAM.
func RegisterRoutes(router *gin.Engine, module *StorageModule) {
	// ------------------------------------------------------------------------
	// 👑 PERSONAL API GROUP (Dành cho Quản trị viên hệ thống & Người dùng cá nhân)
	// ------------------------------------------------------------------------
	personalGroup := router.Group("/api/v1/personal")
	{
		// ========================================================================
		// 🗄️ PHÂN KHÚC BUCKETS: QUẢN TRỊ STORAGE BUCKETS (PERSONAL)
		// ========================================================================
		
		// [COMMENT]: Tạo mới storage bucket
		personalGroup.POST("/storage/buckets",
			middleware.Authorize("storage:bucket:write", module.L1Registry, "*"),
			module.PersonalBucketHandler.Create,
		)

		// [COMMENT]: Liệt kê danh sách tên của tất cả các buckets (truy vấn nhẹ)
		personalGroup.GET("/storage/buckets/names",
			middleware.Authorize("storage:bucket:read", module.L1Registry, "*"),
			module.PersonalBucketHandler.ListNames,
		)

		// [COMMENT]: Lấy chi tiết thông tin bucket
		personalGroup.GET("/storage/buckets/:id",
			middleware.Authorize("storage:bucket:read", module.L1Registry, "*"),
			module.PersonalBucketHandler.Get,
		)

		// [COMMENT]: Liệt kê danh sách các buckets
		personalGroup.GET("/storage/buckets",
			middleware.Authorize("storage:bucket:read", module.L1Registry, "*"),
			module.PersonalBucketHandler.List,
		)

		// [COMMENT]: Cập nhật hạn mức lưu trữ (Quota) của bucket
		personalGroup.PATCH("/storage/buckets/:id/quota",
			middleware.Authorize("storage:bucket:write", module.L1Registry, "*"),
			module.PersonalBucketHandler.UpdateQuota,
		)

		// [COMMENT]: Tạm ngưng hoạt động của bucket
		personalGroup.POST("/storage/buckets/:id/suspend",
			middleware.Authorize("storage:bucket:write", module.L1Registry, "*"),
			module.PersonalBucketHandler.Suspend,
		)

		// [COMMENT]: Kích hoạt lại bucket bị suspend
		personalGroup.POST("/storage/buckets/:id/resume",
			middleware.Authorize("storage:bucket:write", module.L1Registry, "*"),
			module.PersonalBucketHandler.Resume,
		)

		// [COMMENT]: Yêu cầu xóa bucket
		personalGroup.DELETE("/storage/buckets/:id",
			middleware.Authorize("storage:bucket:delete", module.L1Registry, "*"),
			module.PersonalBucketHandler.Delete,
		)

		// ========================================================================
		// 🔑 PHÂN KHÚC CREDENTIALS: QUẢN LÝ ACCESS KEYS (PERSONAL)
		// ========================================================================

		// [COMMENT]: Tạo mới cặp Access Key truy cập bucket
		personalGroup.POST("/storage/buckets/:id/credentials",
			middleware.Authorize("storage:credential:write", module.L1Registry, "*"),
			module.PersonalCredentialHandler.Create,
		)

		// [COMMENT]: Liệt kê các Access Keys của bucket
		personalGroup.GET("/storage/buckets/:id/credentials",
			middleware.Authorize("storage:credential:read", module.L1Registry, "*"),
			module.PersonalCredentialHandler.List,
		)

		// [COMMENT]: Chi tiết một Access Key cụ thể
		personalGroup.GET("/storage/credentials/:id",
			middleware.Authorize("storage:credential:read", module.L1Registry, "*"),
			module.PersonalCredentialHandler.Get,
		)

		// [COMMENT]: Thu hồi / Xóa bỏ Access Key
		personalGroup.DELETE("/storage/credentials/:id",
			middleware.Authorize("storage:credential:delete", module.L1Registry, "*"),
			module.PersonalCredentialHandler.Revoke,
		)
	}

	// ------------------------------------------------------------------------
	// 🏢 TENANT API GROUP (Dành cho ngữ cảnh Tenant)
	// ------------------------------------------------------------------------
	tenantGroup := router.Group("/api/v1/tenant")
	{
		// ========================================================================
		// 🗄️ PHÂN KHÚC BUCKETS: QUẢN TRỊ STORAGE BUCKETS (TENANT)
		// ========================================================================
		
		// [COMMENT]: Tạo mới storage bucket
		tenantGroup.POST("/storage/buckets",
			middleware.Authorize("storage:bucket:write", module.L1Registry, "*"),
			module.TenantBucketHandler.Create,
		)

		// [COMMENT]: Lấy chi tiết thông tin bucket
		tenantGroup.GET("/storage/buckets/:id",
			middleware.Authorize("storage:bucket:read", module.L1Registry, "*"),
			module.TenantBucketHandler.Get,
		)

		// [COMMENT]: Liệt kê danh sách các buckets
		tenantGroup.GET("/storage/buckets",
			middleware.Authorize("storage:bucket:read", module.L1Registry, "*"),
			module.TenantBucketHandler.List,
		)

		// [COMMENT]: Cập nhật hạn mức lưu trữ (Quota) của bucket
		tenantGroup.PATCH("/storage/buckets/:id/quota",
			middleware.Authorize("storage:bucket:write", module.L1Registry, "*"),
			module.TenantBucketHandler.UpdateQuota,
		)

		// [COMMENT]: Tạm ngưng hoạt động của bucket
		tenantGroup.POST("/storage/buckets/:id/suspend",
			middleware.Authorize("storage:bucket:write", module.L1Registry, "*"),
			module.TenantBucketHandler.Suspend,
		)

		// [COMMENT]: Kích hoạt lại bucket bị suspend
		tenantGroup.POST("/storage/buckets/:id/resume",
			middleware.Authorize("storage:bucket:write", module.L1Registry, "*"),
			module.TenantBucketHandler.Resume,
		)

		// [COMMENT]: Yêu cầu xóa bucket
		tenantGroup.DELETE("/storage/buckets/:id",
			middleware.Authorize("storage:bucket:delete", module.L1Registry, "*"),
			module.TenantBucketHandler.Delete,
		)

		// ========================================================================
		// 🔑 PHÂN KHÚC CREDENTIALS: QUẢN LÝ ACCESS KEYS (TENANT)
		// ========================================================================

		// [COMMENT]: Tạo mới cặp Access Key truy cập bucket
		tenantGroup.POST("/storage/buckets/:id/credentials",
			middleware.Authorize("storage:credential:write", module.L1Registry, "*"),
			module.TenantCredentialHandler.Create,
		)

		// [COMMENT]: Liệt kê các Access Keys của bucket
		tenantGroup.GET("/storage/buckets/:id/credentials",
			middleware.Authorize("storage:credential:read", module.L1Registry, "*"),
			module.TenantCredentialHandler.List,
		)

		// [COMMENT]: Chi tiết một Access Key cụ thể
		tenantGroup.GET("/storage/credentials/:id",
			middleware.Authorize("storage:credential:read", module.L1Registry, "*"),
			module.TenantCredentialHandler.Get,
		)

		// [COMMENT]: Thu hồi / Xóa bỏ Access Key
		tenantGroup.DELETE("/storage/credentials/:id",
			middleware.Authorize("storage:credential:delete", module.L1Registry, "*"),
			module.TenantCredentialHandler.Revoke,
		)
	}
}
