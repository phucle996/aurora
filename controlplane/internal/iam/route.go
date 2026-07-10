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
		module.DeviceSelfHandler.ListMyDevices,
	)

	// [COMMENT]: 1.4) Thu hồi quyền truy cập của một thiết bị cụ thể
	router.POST("/api/v1/me/devices/:device_id/revoke",
		module.DeviceSelfHandler.RevokeMyDevice,
	)

	// [COMMENT]: 1.5) Đăng xuất khỏi toàn bộ thiết bị khác ngoại trừ thiết bị hiện tại
	router.POST("/api/v1/me/devices/logout-others",
		module.DeviceSelfHandler.LogoutOtherDevices,
	)



	// [COMMENT]: 1.7) Lấy cấu hình render context cho console UI thông qua platform handler
	router.GET("/api/v1/me/context",
		module.RbacPlatformHandler.GetRenderContext,
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

		// [COMMENT]: Lấy thông tin vai trò của một user cụ thể (yêu cầu quyền iam:users:read và level 2) thông qua platform handler
		personalGroup.GET("/iam/users/:id/roles",
			middleware.Authorize("iam:users:read", module.L1Registry, "2"),
			module.RbacPlatformHandler.GetUserRolesPlatform,
		)

		// [COMMENT]: Lấy trạng thái xác thực MFA của một user cụ thể phục vụ platform audit (yêu cầu quyền iam:mfa:view và level 2)
		personalGroup.GET("/iam/users/:id/mfa",
			middleware.Authorize("iam:mfa:view", module.L1Registry, "2"),
			module.MfaHandler.GetUserMfaPlatform,
		)

		// [COMMENT]: Lấy toàn bộ danh sách platform-scoped roles (yêu cầu quyền iam:role:read và level 2) thông qua platform handler
		personalGroup.GET("/iam/rbac/role",
			middleware.Authorize("iam:role:read", module.L1Registry, "2"),
			module.RbacPlatformHandler.ListRolesPlatform,
		)

		// [COMMENT]: Tạo vai trò hệ thống mới (yêu cầu quyền iam:role:create và level 2) thông qua platform handler
		personalGroup.POST("/iam/rbac/role",
			middleware.Authorize("iam:role:create", module.L1Registry, "2"),
			module.RbacPlatformHandler.CreateRole,
		)

		// [COMMENT]: Lấy danh sách toàn bộ các permissions hệ thống (yêu cầu quyền iam:permissions:read và level 2) thông qua platform handler
		personalGroup.GET("/iam/rbac/permissions",
			middleware.Authorize("iam:permissions:read", module.L1Registry, "2"),
			module.RbacPlatformHandler.ListPermissions,
		)

		// [COMMENT]: Gán vai trò cho User hệ thống thông qua platform handler
		personalGroup.POST("/rbac/user-role",
			module.RbacPlatformHandler.AssignUserRole,
		)

		// [COMMENT]: Gán vai trò cho Tenant hệ thống thông qua platform handler
		personalGroup.POST("/rbac/tenant-role",
			module.RbacPlatformHandler.AssignTenantRole,
		)

		// [COMMENT]: Lấy danh sách thiết bị của một user cụ thể (yêu cầu quyền iam:device:read và level 2) thông qua platform handler
		personalGroup.GET("/iam/users/:id/devices",
			middleware.Authorize("iam:device:read", module.L1Registry, "2"),
			module.DevicePlatformHandler.ListUserDevicesPlatform,
		)
	}

	// ------------------------------------------------------------------------
	// 🏢 TENANT API GROUP (Dành cho ngữ cảnh Tenant, quản trị viên Tenant)
	// ------------------------------------------------------------------------
	tenantGroup := router.Group("/api/v1/tenant")
	{
		// [COMMENT]: Lấy toàn bộ danh sách tenant-scoped roles (yêu cầu quyền iam:role:read và level *) thông qua tenant handler
		tenantGroup.GET("/iam/rbac/role",
			middleware.Authorize("iam:role:read", module.L1Registry, "*"),
			module.RbacTenantHandler.ListRolesTenant,
		)

		// [COMMENT]: Lấy danh sách permissions khả dụng cho Tenant (yêu cầu quyền iam:permissions:read và level *) thông qua platform handler
		tenantGroup.GET("/iam/rbac/permissions",
			middleware.Authorize("iam:permissions:read", module.L1Registry, "*"),
			module.RbacPlatformHandler.ListPermissions,
		)

		// [COMMENT]: Gán vai trò cho User nội bộ Tenant thông qua tenant handler
		tenantGroup.POST("/rbac/user-role",
			module.RbacTenantHandler.AssignUserRole,
		)

		// [COMMENT]: Gán vai trò cho Tenant con thông qua tenant handler
		tenantGroup.POST("/rbac/tenant-role",
			module.RbacTenantHandler.AssignTenantRole,
		)
	}
}
