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
	// ========================================================================

	// 1) Đăng ký tài khoản mới
	router.POST("/api/v1/auth/register",
		module.AuthHandler.RegisterAccount,
	)

	// [COMMENT]: 1.1) Kích hoạt tài khoản mới đăng ký qua link email
	router.GET("/api/v1/auth/verify",
		module.AuthHandler.VerifyAccount,
	)

	// [COMMENT]: 1.2) Lấy danh sách users hệ thống (yêu cầu quyền iam:users:list)
	router.GET("/api/v1/iam/users",
		middleware.Authorize("iam:users:list", module.L1Registry, "2"),
		module.UserHandler.ListUsers,
	)

	// [COMMENT]: 1.3) Xóa user hệ thống (yêu cầu quyền iam:users:delete)
	router.DELETE("/api/v1/iam/users/:id",
		middleware.Authorize("iam:users:delete", module.L1Registry, "2"),
		module.UserHandler.UpdateUserStatus,
	)

	// 5.5) Lấy thông tin cá nhân (Profile) của user hiện tại
	router.GET("/api/v1/me/profile",
		module.UserHandler.GetMyProfile,
	)

	// 6) Quản lý thiết bị cá nhân
	router.GET("/api/v1/me/devices",
		module.DeviceHandler.ListMyDevices,
	)

	// 7) Thu hồi quyền truy cập của một thiết bị cụ thể
	router.POST("/api/v1/me/devices/:device_id/revoke",
		module.DeviceHandler.RevokeMyDevice,
	)

	// 8) Đăng xuất khỏi toàn bộ thiết bị khác ngoại trừ thiết bị hiện tại
	router.POST("/api/v1/me/devices/logout-others",
		module.DeviceHandler.LogoutOtherDevices,
	)

	// 9) Đăng xuất hoàn toàn trên toàn bộ thiết bị
	router.POST("/api/v1/me/devices/logout-all",
		module.DeviceHandler.LogoutAllDevices,
	)

	// ========================================================================
	// 🔑 PHÂN KHÚC RBAC: QUẢN LÝ VAI TRÒ & PHÂN QUYỀN (ADMIN ROLE-BASED ACCESS CONTROL)
	// ========================================================================

	// [COMMENT]: 15) Gán vai trò cho User trong workspace
	router.POST("/api/v1/rbac/user-role",
		module.RbacHandler.AssignUserRole,
	)

	// [COMMENT]: 16) Gán vai trò cho Tenant trong workspace
	router.POST("/api/v1/rbac/tenant-role",
		module.RbacHandler.AssignTenantRole,
	)

	// [COMMENT]: 17) Lấy toàn bộ danh sách platform-scoped roles (yêu cầu quyền iam:role:list và level 2)
	router.GET("/api/v1/iam/rbac/role",
		middleware.Authorize("iam:role:list", module.L1Registry, "2"),
		module.RbacHandler.ListPlatformRoles,
	)

	// [COMMENT]: 18) Lấy toàn bộ danh sách tenant-scoped roles của tenant cụ thể (yêu cầu quyền iam:role:list và level *)
	router.GET("/api/v1/iam/rbac/role/tenant",
		middleware.Authorize("iam:role:list", module.L1Registry, "*"),
		module.RbacHandler.ListTenantRoles,
	)

	// [COMMENT]: 19) Lấy cấu hình render context cho console UI (chỉ yêu cầu auth session, bypass authz check)
	router.GET("/api/v1/me/context",
		module.RbacHandler.GetRenderContext,
	)
}
