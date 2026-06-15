// ============================================================================
// 🔒 ARCHITECTURAL COMPONENT: IAM ROUTING & SECURITY POLICIES
// ============================================================================
// Thiết kế bởi: Antigravity AI & SRE Platform Engineering Team.
//
// 📜 SOVEREIGN CONTRACT (Hợp đồng Tối cao) & CHỨC NĂNG CHÍNH:
//   - Tệp tin này đóng vai trò là **IAM ROUTING REGISTRY & SECURITY BOUNDARY**
//     (Trình đăng ký định tuyến và Ranh giới bảo mật của phân hệ IAM).
//   - Nhiệm vụ cốt lõi: Phơi bày (expose) các API endpoints phục vụ cho quá trình
//     xác thực người dùng (User Authentication), quản lý phiên (Session Management),
//     thiết bị (Device Management) và phân quyền quản trị viên (Admin RBAC).
//
// 🛡️ ARCHITECTURAL BOUNDARY (Ranh giới Kiến trúc & Chuỗi Middlewares Bảo mật):
//   Hệ thống phân tách rõ rệt các ranh giới bảo mật cho từng nhóm đối tượng sử dụng:
//
//   🟢 PHÂN HẠNG 1: USER ENDPOINTS (/api/v1/auth/*, /api/v1/me/*)
//     - Định danh & Xác thực cơ bản: Sử dụng các HTTP Post-Auth Rate Limiters để chống bruteforce.
//     - Phiên làm việc (Session Guard): Được bảo vệ qua `middleware.Access()` nhằm kiểm định
//       tính hợp lệ của JWT token đồng thời liên kết trực tiếp với Device Runtime Cache trong Redis.
//
//   🔵 PHÂN HẠNG 2: ADMIN ENDPOINTS (/admin/*)
//     - Ranh giới mạng (CIDR Restriction): Enforce kiểm tra whitelist IP thông qua `AdminCIDR()`
//       tại các route quản trị và cấu hình nhạy cảm.
//     - Xác thực API Key: Sử dụng `AdminAPIKeyAuth()` đi kèm các tuỳ chọn tiêm thông tin truy cập
//       động (AccessKey, AccessSecret) vào context.
//     - Ký số chống giả mạo (Cryptographic Signature): Tích hợp `AdminCriticalSignature()` yêu cầu
//       ký số phi đối xứng từ cặp key bảo mật của admin thiết bị, ngăn chặn replay/MitM attacks.
//     - Xác thực hai yếu tố nâng cao (Step-Up 2FA): Route thay đổi cấu hình bảo mật cực kỳ nguy hiểm
//       bắt buộc đi qua `AdminCriticalStepUp2FA()` để verify OTP/Hardware Token của Admin.
//
// 🚀 HIGH-AVAILABILITY (HA) & OBSERVED RATE LIMITING:
//   - Rate Limiting được quản lý tập trung và phân tán trên cụm Redis để bảo đảm khả năng
//     scaling ngang (horizontal scale) của hệ thống Cloud-Native Control Plane.
// ============================================================================

package iam

import (
	"controlplane/internal/http/middleware"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes thực hiện đăng ký và thiết lập chuỗi phòng ngự (Security Chain) cho toàn bộ IAM API.
func RegisterRoutes(router *gin.Engine, module *IAMModule) {

	// ========================================================================
	// 👥 PHÂN KHÚC USER: ĐĂNG KÝ, ĐĂNG NHẬP & PHIÊN LÀM VIỆC (USER AUTH & DEVICES)
	// ========================================================================

	// 1) Đăng ký tài khoản mới: Yêu cầu giới hạn tần suất nghiêm ngặt (ví dụ: 5 req/min).
	// Do là route Pre-Auth (chưa xác thực), RateLimitPostAuth sẽ tự động fallback về
	// cơ chế giới hạn theo IP thô (KeyIP) nhưng vẫn áp dụng đúng rule cấu hình cho path này.
	router.POST("/api/v1/auth/register",
		middleware.RateLimitPostAuth(module.rateLimiter, "/api/v1/auth/register"),
		module.AuthHandler.RegisterAccount,
	)

	// 2) Đăng nhập tài khoản: Giới hạn brute-force đăng nhập.
	// Tương tự, hoạt động ở chế độ fallback IP-only do chưa có thông tin identity.
	router.POST("/api/v1/auth/login",
		middleware.RateLimitPostAuth(module.rateLimiter, "/api/v1/auth/login"),
		module.AuthHandler.Login,
	)

	// 3) Làm mới Token (Refresh): Yêu cầu định danh qua Access Guard & Rate Limit
	router.POST("/api/v1/auth/refresh",
		middleware.Access(),
		middleware.ZoneRequired(),
		middleware.RateLimitPostAuth(module.rateLimiter, "/api/v1/auth/refresh"),
		module.RefreshTokenHandler.Refresh,
	)

	// 4) Lấy thông tin phiên làm việc hiện tại
	router.GET("/api/v1/auth/session",
		middleware.Access(),
		middleware.RateLimitPostAuth(module.rateLimiter, "/api/v1/auth/session"),
		module.AuthHandler.Session,
	)

	// 5) Đăng xuất tài khoản
	router.POST("/api/v1/auth/logout",
		middleware.Access(),
		middleware.RateLimitPostAuth(module.rateLimiter, "/api/v1/auth/logout"),
		module.AuthHandler.Logout,
	)

	// 6) Quản lý thiết bị cá nhân
	router.GET("/api/v1/me/devices",
		middleware.Access(),
		middleware.RateLimitPostAuth(module.rateLimiter, "/api/v1/me/devices"),
		module.DeviceHandler.ListMyDevices,
	)

	// 7) Thu hồi quyền truy cập của một thiết bị cụ thể
	router.POST("/api/v1/me/devices/:device_id/revoke",
		middleware.Access(),
		middleware.RateLimitPostAuth(module.rateLimiter, "/api/v1/me/devices/:device_id/revoke"),
		module.DeviceHandler.RevokeMyDevice,
	)

	// 8) Đăng xuất khỏi toàn bộ thiết bị khác ngoại trừ thiết bị hiện tại
	router.POST("/api/v1/me/devices/logout-others",
		middleware.Access(),
		middleware.RateLimitPostAuth(module.rateLimiter, "/api/v1/me/devices/logout-others"),
		module.DeviceHandler.LogoutOtherDevices,
	)

	// 9) Đăng xuất hoàn toàn trên toàn bộ thiết bị
	router.POST("/api/v1/me/devices/logout-all",
		middleware.Access(),
		middleware.RateLimitPostAuth(module.rateLimiter, "/api/v1/me/devices/logout-all"),
		module.DeviceHandler.LogoutAllDevices,
	)

	// ========================================================================
	// 👑 PHÂN KHÚC ADMIN: QUẢN TRỊ VIÊN & RBAC (ADMIN AUTH, SECURITY & ROLES)
	// ========================================================================

	// 10) Đăng nhập Admin
	router.POST("/admin/auth/login",
		middleware.AdminCIDR(),
		middleware.RateLimitPostAuth(module.rateLimiter, "/admin/auth/login"),
		module.AdminAuthHandler.Login,
	)

	// 11) Lấy thông tin phiên làm việc hiện tại của Admin: Bảo vệ bởi Admin API Key
	router.GET("/admin/auth/session",
		middleware.AdminAPIKeyAuth(),
		middleware.ZoneOptional(),
		middleware.RateLimitPostAuth(module.rateLimiter, "/admin/auth/session"),
		module.AdminAuthHandler.Session,
	)

	// 12) Đăng xuất Admin: Thu hồi và giải phóng Access Key
	router.POST("/admin/auth/logout",
		middleware.AdminAPIKeyAuth(
			middleware.WithInjectAccessKey(),
		),
		middleware.RateLimitPostAuth(module.rateLimiter, "/admin/auth/logout"),
		module.AdminAuthHandler.Logout,
	)

	// 13) Làm mới Token Admin: Giới hạn bởi IP Whitelist (CIDR) & Xác thực API Key chuyên sâu
	router.POST("/admin/auth/refresh",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(
			middleware.WithInjectAccessKey(),
			middleware.WithInjectAccessSecret(),
		),
		middleware.ZoneOptional(),
		middleware.RateLimitPostAuth(module.rateLimiter, "/admin/auth/refresh"),
		module.AdminAuthHandler.Refresh,
	)

	// 14) Xoay vòng khoá bảo mật Admin (Rotate Key) - HÀNH ĐỘNG CỰC KỲ NHẠY CẢM:
	//     - Bắt buộc kiểm tra CIDR Whitelist.
	//     - Đăng nhập/Xác thực API Key.
	//     - Rate Limiting chặt chẽ.
	//     - Ký số chống giả mạo (Signature Verification).
	//     - Xác thực 2 yếu tố Step-Up 2FA.
	router.POST("/admin/auth/rotate-key",
		middleware.AdminCIDR(),
		middleware.AdminAPIKeyAuth(
			middleware.WithInjectAccessKey(),
		),
		middleware.RateLimitPostAuth(module.rateLimiter, "/admin/auth/rotate-key"),
		middleware.AdminCriticalSignature(),
		middleware.AdminCriticalStepUp2FA(),
		module.AdminAuthHandler.RotateKey,
	)

	// ========================================================================
	// 🔑 PHÂN KHÚC RBAC: QUẢN LÝ VAI TRÒ & PHÂN QUYỀN (ADMIN ROLE-BASED ACCESS CONTROL)
	// ========================================================================

	// 15) Liệt kê danh sách vai trò
	router.GET("/api/v1/rbac/roles",
		middleware.Access(),
		middleware.RateLimitPostAuth(module.rateLimiter, "/api/v1/rbac/roles"),
		module.RbacHandler.ListRoles,
	)

	// 16) Tạo vai trò mới
	router.POST("/api/v1/rbac/roles",
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.rateLimiter, "/api/v1/rbac/roles"),
		module.RbacHandler.CreateRole,
	)

	// 17) Cập nhật cấu hình vai trò
	router.PUT("/api/v1/rbac/roles/:id",
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.rateLimiter, "/api/v1/rbac/roles/:id"),
		module.RbacHandler.UpdateRole,
	)

	// 18) Xóa bỏ vai trò
	router.DELETE("/api/v1/rbac/roles/:id",
		middleware.AdminAPIKeyAuth(),
		middleware.RateLimitPostAuth(module.rateLimiter, "/api/v1/rbac/roles/:id"),
		module.RbacHandler.DeleteRole,
	)
}
