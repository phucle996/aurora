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
	// ========================================================================
	// 🗄️ PHÂN KHÚC BUCKETS: QUẢN TRỊ STORAGE BUCKETS (PERSONAL)
	// ========================================================================

	// [COMMENT]: Tạo mới storage bucket
	router.POST("/api/v1/personal/critical/storage/buckets",
		middleware.RequireSessionProof(),
		middleware.Authorize("storage:bucket:write", module.L1Registry, "*"),
		module.PersonalBucketHandler.Create,
	)

	// [COMMENT]: Liệt kê danh sách tên của tất cả các buckets (truy vấn nhẹ)
	router.GET("/api/v1/personal/storage/buckets/names",
		middleware.Authorize("storage:bucket:read", module.L1Registry, "*"),
		module.PersonalBucketHandler.ListNames,
	)

	// [COMMENT]: Lấy chi tiết thông tin bucket
	router.GET("/api/v1/personal/storage/buckets/:id",
		middleware.Authorize("storage:bucket:read", module.L1Registry, "*"),
		module.PersonalBucketHandler.Get,
	)

	// [COMMENT]: Liệt kê danh sách các buckets
	router.GET("/api/v1/personal/storage/buckets",
		middleware.Authorize("storage:bucket:read", module.L1Registry, "*"),
		module.PersonalBucketHandler.List,
	)

	// [COMMENT]: Cập nhật hạn mức lưu trữ (Quota) của bucket
	router.PATCH("/api/v1/personal/critical/storage/buckets/:id/quota",
		middleware.RequireSessionProof(),
		middleware.Authorize("storage:bucket:write", module.L1Registry, "*"),
		module.PersonalBucketHandler.UpdateQuota,
	)

	// [COMMENT]: Cập nhật trạng thái Versioning của bucket
	router.PATCH("/api/v1/personal/critical/storage/buckets/:id/versioning",
		middleware.RequireSessionProof(),
		middleware.Authorize("storage:bucket:write", module.L1Registry, "*"),
		module.PersonalBucketHandler.UpdateVersioning,
	)

	// [COMMENT]: Lấy cấu hình Lifecycle của bucket
	router.GET("/api/v1/personal/storage/buckets/:id/lifecycle",
		middleware.Authorize("storage:bucket:read", module.L1Registry, "*"),
		module.PersonalBucketHandler.GetLifecycle,
	)

	// [COMMENT]: Cập nhật cấu hình Lifecycle của bucket
	router.PUT("/api/v1/personal/critical/storage/buckets/:id/lifecycle",
		middleware.RequireSessionProof(),
		middleware.Authorize("storage:bucket:write", module.L1Registry, "*"),
		module.PersonalBucketHandler.UpdateLifecycle,
	)

	// [COMMENT]: Yêu cầu xóa bucket
	router.DELETE("/api/v1/personal/critical/storage/buckets/:id",
		middleware.RequireSessionProof(),
		middleware.Authorize("storage:bucket:delete", module.L1Registry, "*"),
		module.PersonalBucketHandler.Delete,
	)

	// Metadata-only session. ACR authenticates the Trinity cookie and the
	// Zone Gateway verifies the Central assertion; no client secret is issued.
	router.POST("/api/v1/personal/storage/buckets/:id/access-sessions",
		middleware.Authorize("storage:bucket:read", module.L1Registry, "*"),
		module.PersonalBucketHandler.CreateAccessSession,
	)
	router.GET("/api/v1/personal/storage/buckets/:id/access-sessions/:access_session_id",
		middleware.Authorize("storage:bucket:read", module.L1Registry, "*"),
		module.PersonalBucketHandler.GetAccessSessionStatus,
	)

	// ========================================================================
	// 🔑 PHÂN KHÚC CREDENTIALS: QUẢN LÝ ACCESS KEYS (PERSONAL)
	// ========================================================================

	// [COMMENT]: Tạo mới cặp Access Key truy cập bucket
	router.POST("/api/v1/personal/critical/storage/buckets/:id/credentials",
		middleware.RequireSessionProof(),
		middleware.Authorize("storage:credential:write", module.L1Registry, "*"),
		module.PersonalCredentialHandler.Create,
	)

	// [COMMENT]: Liệt kê các Access Keys của bucket
	router.GET("/api/v1/personal/storage/buckets/:id/credentials",
		middleware.Authorize("storage:credential:read", module.L1Registry, "*"),
		module.PersonalCredentialHandler.List,
	)

	// [COMMENT]: Xóa bỏ Access Key
	router.DELETE("/api/v1/personal/critical/storage/buckets/:id/credentials/:credential_id",
		middleware.RequireSessionProof(),
		middleware.Authorize("storage:credential:delete", module.L1Registry, "*"),
		module.PersonalCredentialHandler.Delete,
	)

	// ========================================================================
	// 📦 PHÂN KHÚC OBJECTS: QUẢN LÝ ĐỐI TƯỢNG (PERSONAL)
	// ========================================================================

	// ------------------------------------------------------------------------
	// 🏢 TENANT API GROUP (Dành cho ngữ cảnh Tenant)
	// ------------------------------------------------------------------------
	// ========================================================================
	// 🗄️ PHÂN KHÚC BUCKETS: QUẢN TRỊ STORAGE BUCKETS (TENANT)
	// ========================================================================

	// [COMMENT]: Tạo mới storage bucket
	router.POST("/api/v1/tenant/critical/storage/buckets",
		middleware.RequireSessionProof(),
		middleware.Authorize("storage:bucket:write", module.L1Registry, "*"),
		module.TenantBucketHandler.Create,
	)

	// [COMMENT]: Lấy chi tiết thông tin bucket
	router.GET("/api/v1/tenant/storage/buckets/:id",
		middleware.Authorize("storage:bucket:read", module.L1Registry, "*"),
		module.TenantBucketHandler.Get,
	)

	// [COMMENT]: Liệt kê danh sách các buckets
	router.GET("/api/v1/tenant/storage/buckets",
		middleware.Authorize("storage:bucket:read", module.L1Registry, "*"),
		module.TenantBucketHandler.List,
	)

	// [COMMENT]: Cập nhật hạn mức lưu trữ (Quota) của bucket
	router.PATCH("/api/v1/tenant/critical/storage/buckets/:id/quota",
		middleware.RequireSessionProof(),
		middleware.Authorize("storage:bucket:write", module.L1Registry, "*"),
		module.TenantBucketHandler.UpdateQuota,
	)

	// [COMMENT]: Cập nhật trạng thái Versioning của bucket
	router.PATCH("/api/v1/tenant/critical/storage/buckets/:id/versioning",
		middleware.RequireSessionProof(),
		middleware.Authorize("storage:bucket:write", module.L1Registry, "*"),
		module.TenantBucketHandler.UpdateVersioning,
	)

	// [COMMENT]: Lấy cấu hình Lifecycle của bucket
	router.GET("/api/v1/tenant/storage/buckets/:id/lifecycle",
		middleware.Authorize("storage:bucket:read", module.L1Registry, "*"),
		module.TenantBucketHandler.GetLifecycle,
	)

	// [COMMENT]: Cập nhật cấu hình Lifecycle của bucket
	router.PUT("/api/v1/tenant/critical/storage/buckets/:id/lifecycle",
		middleware.RequireSessionProof(),
		middleware.Authorize("storage:bucket:write", module.L1Registry, "*"),
		module.TenantBucketHandler.UpdateLifecycle,
	)

	// [COMMENT]: Yêu cầu xóa bucket
	router.DELETE("/api/v1/tenant/critical/storage/buckets/:id",
		middleware.RequireSessionProof(),
		middleware.Authorize("storage:bucket:delete", module.L1Registry, "*"),
		module.TenantBucketHandler.Delete,
	)

	// ========================================================================
	// 🔑 PHÂN KHÚC CREDENTIALS: QUẢN LÝ ACCESS KEYS (TENANT)
	// ========================================================================

	// [COMMENT]: Tạo mới cặp Access Key truy cập bucket
	router.POST("/api/v1/tenant/critical/storage/buckets/:id/credentials",
		middleware.RequireSessionProof(),
		middleware.Authorize("storage:credential:write", module.L1Registry, "*"),
		module.TenantCredentialHandler.Create,
	)

	// [COMMENT]: Liệt kê các Access Keys của bucket
	router.GET("/api/v1/tenant/storage/buckets/:id/credentials",
		middleware.Authorize("storage:credential:read", module.L1Registry, "*"),
		module.TenantCredentialHandler.List,
	)

	// [COMMENT]: Xóa bỏ Access Key
	router.DELETE("/api/v1/tenant/critical/storage/buckets/:id/credentials/:credential_id",
		middleware.RequireSessionProof(),
		middleware.Authorize("storage:credential:delete", module.L1Registry, "*"),
		module.TenantCredentialHandler.Delete,
	)
}
