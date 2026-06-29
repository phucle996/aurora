package iam

import (
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

	// 15) Liệt kê danh sách vai trò
	router.GET("/api/v1/rbac/roles",
		module.RbacHandler.ListRoles,
	)

	// 16) Tạo vai trò mới
	router.POST("/api/v1/rbac/roles",
		module.RbacHandler.CreateRole,
	)

	// 17) Cập nhật cấu hình vai trò
	router.PUT("/api/v1/rbac/roles/:id",
		module.RbacHandler.UpdateRole,
	)

	// 18) Xóa bỏ vai trò
	router.DELETE("/api/v1/rbac/roles/:id",
		module.RbacHandler.DeleteRole,
	)
}
