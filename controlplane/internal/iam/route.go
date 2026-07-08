package iam

import (
	"controlplane/internal/http/middleware"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes thực hiện đăng ký và thiết lập chuỗi phòng ngự (Security Chain) cho toàn bộ IAM API.
// [COMMENT]: Đã loại bỏ hoàn toàn RateLimitPostAuth middleware tại đây.
// Toàn bộ logic giới hạn tần suất (Rate Limiting) hiện đã được bàn giao (offload)
// lên tầng Rust ACR (Edge) chạy trước Envoy để tăng tính HA và giảm tải CPU/Redis cho Control Plane.
func RegisterRoutes(router *gin.Engine, module *IAMModule) {

	// ========================================================================
	// 👥 PHÂN KHÚC USER: ĐĂNG KÝ, ĐĂNG NHẬP & PHIÊN LÀM VIỆC (USER AUTH & DEVICES)
	// [COMMENT]: Các API cá nhân và Auth này bypass cơ chế path rewrite của Envoy/ACR
	// ========================================================================

	// 1) Đăng ký tài khoản mới
	router.POST("/api/v1/auth/register",
		module.AuthHandler.RegisterAccount,
	)

	// [COMMENT]: 1.1) Kích hoạt tài khoản mới đăng ký qua link email
	router.GET("/api/v1/auth/verify",
		module.AuthHandler.VerifyAccount,
	)

	// [COMMENT]: 1.2) Lấy thông tin cá nhân (Profile) của user hiện tại
	router.GET("/api/v1/me/profile",
		module.UserHandler.GetMyProfile,
	)

	// [COMMENT]: 1.3) Quản lý thiết bị cá nhân
	router.GET("/api/v1/me/devices",
		module.DeviceHandler.ListMyDevices,
	)

	// [COMMENT]: 1.4) Thu hồi quyền truy cập của một thiết bị cụ thể
	router.POST("/api/v1/me/devices/:device_id/revoke",
		module.DeviceHandler.RevokeMyDevice,
	)

	// [COMMENT]: 1.5) Đăng xuất khỏi toàn bộ thiết bị khác ngoại trừ thiết bị hiện tại
	router.POST("/api/v1/me/devices/logout-others",
		module.DeviceHandler.LogoutOtherDevices,
	)

	// [COMMENT]: 1.6) Đăng xuất hoàn toàn trên toàn bộ thiết bị
	router.POST("/api/v1/me/devices/logout-all",
		module.DeviceHandler.LogoutAllDevices,
	)

	// [COMMENT]: 1.7) Lấy cấu hình render context cho console UI
	router.GET("/api/v1/me/context",
		module.RbacHandler.GetRenderContext,
	)

	// ========================================================================
	// 🏢 PHÂN HỆ PHÂN LẬP: PLATFORM API (Cho Admin) & TENANT API (Cho Tenant)
	// [COMMENT]: Envoy và Rust ACR sẽ tự động rewrite path thành /platform/...
	// hoặc /tenant/... tùy thuộc vào ngữ cảnh session và cookie hợp lệ.
	// ========================================================================

	// ------------------------------------------------------------------------
	// 👑 PERSONAL API GROUP (Dành cho Quản trị viên hệ thống & Người dùng cá nhân)
	// ------------------------------------------------------------------------
	personalGroup := router.Group("/api/v1/personal")
	{
		// [COMMENT]: Lấy danh sách users hệ thống (yêu cầu quyền iam:users:read và level 2)
		personalGroup.GET("/iam/users",
			middleware.Authorize("iam:users:read", module.L1Registry, "2"),
			module.UserHandler.ListUsersPlatform,
		)

		// [COMMENT]: Xóa/Cập nhật trạng thái user hệ thống (yêu cầu quyền iam:users:delete và level 2)
		personalGroup.DELETE("/iam/users/:id",
			middleware.Authorize("iam:users:delete", module.L1Registry, "2"),
			module.UserHandler.UpdateUserStatusPlatform,
		)

		// [COMMENT]: Lấy toàn bộ danh sách platform-scoped roles (yêu cầu quyền iam:role:read và level 2)
		personalGroup.GET("/iam/rbac/role",
			middleware.Authorize("iam:role:read", module.L1Registry, "2"),
			module.RbacHandler.ListRolesPlatform,
		)

		// [COMMENT]: Tạo vai trò hệ thống mới (yêu cầu quyền iam:role:create và level 2)
		personalGroup.POST("/iam/rbac/role",
			middleware.Authorize("iam:role:create", module.L1Registry, "2"),
			module.RbacHandler.CreateRole,
		)

		// [COMMENT]: Lấy danh sách toàn bộ các permissions hệ thống (yêu cầu quyền iam:permissions:read và level 2)
		personalGroup.GET("/iam/rbac/permissions",
			middleware.Authorize("iam:permissions:read", module.L1Registry, "2"),
			module.RbacHandler.ListPermissions,
		)

		// [COMMENT]: Gán vai trò cho User hệ thống
		personalGroup.POST("/rbac/user-role",
			module.RbacHandler.AssignUserRole,
		)

		// [COMMENT]: Gán vai trò cho Tenant hệ thống
		personalGroup.POST("/rbac/tenant-role",
			module.RbacHandler.AssignTenantRole,
		)
	}

	// ------------------------------------------------------------------------
	// 🏢 TENANT API GROUP (Dành cho ngữ cảnh Tenant, quản trị viên Tenant)
	// ------------------------------------------------------------------------
	tenantGroup := router.Group("/api/v1/tenant")
	{
		// [COMMENT]: Lấy toàn bộ danh sách tenant-scoped roles (yêu cầu quyền iam:role:read và level *)
		tenantGroup.GET("/iam/rbac/role",
			middleware.Authorize("iam:role:read", module.L1Registry, "*"),
			module.RbacHandler.ListRolesTenant,
		)

		// [COMMENT]: Lấy danh sách permissions khả dụng cho Tenant (yêu cầu quyền iam:permissions:read và level *)
		tenantGroup.GET("/iam/rbac/permissions",
			middleware.Authorize("iam:permissions:read", module.L1Registry, "*"),
			module.RbacHandler.ListPermissions,
		)

		// [COMMENT]: Gán vai trò cho User nội bộ Tenant
		tenantGroup.POST("/rbac/user-role",
			module.RbacHandler.AssignUserRole,
		)

		// [COMMENT]: Gán vai trò cho Tenant con
		tenantGroup.POST("/rbac/tenant-role",
			module.RbacHandler.AssignTenantRole,
		)
	}
}
