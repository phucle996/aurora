// ============================================================================
// 🗺️ ARCHITECTURAL COMPONENT: GLOBAL ROUTE ORCHESTRATOR
// ============================================================================
// Thiết kế bởi: Antigravity AI & SRE Platform Engineering Team.
//
// 📜 SOVEREIGN CONTRACT (Hợp đồng Tối cao) & CHỨC NĂNG CHÍNH:
//   - File `route.go` đóng vai trò là **ROOT OF INITIALIZE ROUTE** (Gốc khởi tạo định tuyến)
//     cho toàn bộ tất cả các module nghiệp vụ chạy trong phân hệ Control Plane.
//   - Chức năng cốt lõi: Tiếp nhận Engine định tuyến trung tâm (`*gin.Engine`), thực hiện
//     phân phối và liên kết (wire) Engine này với các chương trình đăng ký định tuyến
//     cục bộ của từng module (`iam`, `core`, v.v.). Nó hoạt động như một REST API Gateway
//     Facade tập trung ở cấp độ ứng dụng.
//
// 🗄️ SOURCE OF TRUTH - SoT (Nguồn dữ liệu gốc):
//   - [SOT for Global API Path Layout (Root of Routing)]:
//     - Tệp tin này chính là SOURCE OF TRUTH duy nhất xác định toàn cảnh cấu trúc URL định tuyến,
//       thứ tự ưu tiên nạp route và cấu hình phân nhánh API toàn cục của Control Plane.
//     - Bất kỳ API endpoint mới nào được expose cho Client/API Gateway đều phải
//       được đăng ký thông qua đồ thị liên kết tại tệp tin này.
//
// 🛡️ ARCHITECTURAL BOUNDARY (Ranh giới Kiến trúc):
//   - Ranh giới Phơi bày (Exposure Boundary): Phân tách hoàn toàn Engine xử lý HTTP bên ngoài
//     với các business logic khép kín bên trong các package con.
//   - Ranh giới Cô lập Module (Module Isolation Boundary): Enforce nguyên tắc các module
//     không được phép tự ý tiêm (inject) chéo tuyến API của nhau. Tất cả phải đi qua Root
//     tại file này để duy trì tính độc lập hoàn toàn của package.
//
// 💎 CORE ARCHITECTURAL VALUE (Giá trị Kiến trúc & Tầm quan trọng Cốt lõi):
//   1. [Single Point of API Visibility - Điểm Nhìn Định Tuyến Tập Trung]:
//      - Tệp tin này cung cấp một bản đồ toàn cảnh (birds-eye view) duy nhất giúp Tech Lead,
//        SRE và DevOps có thể ngay lập tức nắm bắt toàn bộ bề mặt API (Attack Surface/Exposed APIs)
//        của toàn hệ thống Control Plane mà không cần lục tìm sâu trong từng thư mục mã nguồn.
//   2. [Registry Enforcement - Ngăn chặn Tuyến Đường Ngầm]:
//      - Đóng vai trò là chốt chặn kiểm soát duy nhất. Bằng cách tập trung hóa quá trình khởi tạo,
//        hệ thống triệt tiêu hoàn toàn rủi ro các lập trình viên vô tình phơi ra các API nghiệp vụ
//        không an toàn (Shadow APIs) hoặc bỏ qua các lớp Middleware bảo mật bắt buộc (JWT Access, Rate limit).
//   3. [Middleware & Lifecycle Segregation - Cách ly Vòng đời Middlewares]:
//      - Thiết lập ranh giới phân tầng rõ ràng cho chuỗi Middleware. Việc tách biệt cho phép các route
//        thăm dò hệ thống (Kube Probes) được đăng ký trực tiếp ở mức Root để phản hồi lập tức (Latency < 1ms),
//        trong khi các API nghiệp vụ khác được bảo vệ khép kín trong các Group Router riêng biệt kèm theo
//        chuỗi kiểm tra bảo mật (rate limiting, authentication).
//   4. [Zero-Coupling Clean DI - Khử Phụ Thuộc Chéo]:
//      - Hỗ trợ mô hình tiêm phụ thuộc (Dependency Injection) sạch sẽ. Các Handler chỉ cần quan tâm đến
//        logic xử lý HTTP Request của riêng mình. Sự liên kết giữa Engine định tuyến Gin và các Handler
//        được quyết định hoàn toàn tại đây, giúp các module có thể Mocking độc lập để Unit Test cực kỳ dễ dàng.
//
// 👥 VAI TRÒ VÀ GHI CHÚ VẬN HÀNH (ROLE-SPECIFIC CHEATSHEET):
//
//   📌 ĐỐI VỚI SRE & DEVOPS PLATFORM ENGINEERS:
//     * Ingress & Load Balancer Integration:
//       - SRE Warning: Nếu thay đổi tiền tố API path (`/api/v1/health`), BẮT BUỘC phải cập nhật đồng bộ các tham số cấu hình
//         tương ứng tại Helm Chart/K8s Manifest (`deployment.yaml` -> `livenessProbe.httpGet.path`).
//
//   📌 ĐỐI VỚI TECH LEADS:
//     * Quản lý Mở rộng & Tránh Vòng Lặp Phụ Thuộc (Circular Dependency):
//       - Khi phát triển một phân hệ mới (ví dụ: `auditlog`), không khai báo và khởi tạo router cục bộ bên ngoài.
//       - Luôn tuân thủ mẫu: Tạo tệp `route.go` bên trong module mới và khai báo phương thức đăng ký:
//         `RegisterRoutes(router *gin.Engine, module *Module)`
//       - Sau đó liên kết phương thức này tại đây dưới dạng một khối kiểm tra độc lập `if m.NewModule != nil`.
//
//   📌 ĐỐI VỚI APPLICATION DEVELOPERS:
//     * Quy tắc Đăng ký Route cục bộ:
//       - Tuyệt đối không đăng ký ad-hoc các route nghiệp vụ trực tiếp vào hàm `RegisterRoutes` của file này.
//       - Mọi thao tác bổ sung API, chỉnh sửa Path Variables hay tích hợp Middlewares cục bộ (JWT Access Guard, Rate Limiter)
//         phải được thực hiện khép kín bên trong `iam/route.go` hoặc `core/route.go`.
// ============================================================================

package app

import (
	"controlplane/internal/core"
	"controlplane/internal/hypervisor"
	"controlplane/internal/iam"
	"controlplane/internal/mail"
	"controlplane/pkg/apires"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes thực hiện lập đồ thị và đăng ký toàn bộ định tuyến cho các phân hệ của Control Plane.
func RegisterRoutes(router *gin.Engine, m *Modules) {
	// SRE & Tech Lead Ghi chú: File này tuân thủ tuyệt đối quy tắc Fail-Fast ở tầng biên.
	// Chúng ta KHÔNG thực hiện kiểm tra nil cho 'm' hoặc các thành phần con (m.Health, m.IAM, m.Core).
	// Nếu đồ thị dependency bị lỗi hoặc thiếu hụt, NewGlobalModules (internal/app/module.go)
	// đã trả lỗi ngược lên để Caller dừng tiến trình ngay lập tức. Nếu chương trình chạm tới đây,
	// m và các module con được đảm bảo 100% là hợp lệ và sẵn sàng sử dụng.
	if router == nil {
		return
	}

	// ------------------------------------------------------------------------
	// 🟢 HẠNG MỤC 1: KUBERNETES PROBES & SYSTEM HEALTH TASKS
	// ------------------------------------------------------------------------
	// SRE CRITICAL: Đăng ký trước tiên để bảo đảm thời gian phản hồi (Latency P99 < 1ms)
	// cho hạ tầng giám sát Kubernetes, bỏ qua mọi logic nghiệp vụ phức tạp.
	router.GET("/api/v1/health/liveness", m.Health.Liveness)
	router.GET("/api/v1/health/readiness", m.Health.Readiness)
	router.GET("/api/v1/health/startup", m.Health.Startup)

	// ------------------------------------------------------------------------
	// 🔒 HẠNG MỤC 2: IAM MODULE ROUTING (AUTHENTICATION & AUTHORIZATION)
	// ------------------------------------------------------------------------
	// Ủy quyền đăng ký toàn bộ tuyến API bảo mật cho IAM Module Container.
	iam.RegisterRoutes(router, m.IAM)

	// ------------------------------------------------------------------------
	// 🌐 HẠNG MỤC 3: CORE HẠ TẦNG NGHIỆP VỤ MODULE
	// ------------------------------------------------------------------------
	// Ủy quyền đăng ký toàn bộ tuyến API quản trị hệ thống cốt lõi cho Core Module Container.
	core.RegisterRoutes(router, m.Core)

	// ------------------------------------------------------------------------
	// 🌐 HẠNG MỤC 4: HYPERVISOR VỆ TINH MODULE (TIER-1 DEGRADED-RESILIENT ROUTING)
	// ------------------------------------------------------------------------
	// SRE HA Design Pattern: Nếu module Hypervisor bị degraded/disabled, chúng ta không ẩn
	// hoàn toàn endpoint (gây lỗi 404 khó hiểu cho client), mà tự động đăng ký Fallback Route
	// trả về mã lỗi 503 Service Unavailable chuẩn cấu hình định dạng của apires.
	if m.Hypervisor != nil && m.Hypervisor.IsEnabled() {
		hypervisor.RegisterRoutes(router, m.Hypervisor)
	} else {
		fallbackGroup := router.Group("/api/v1/hypervisor")
		fallbackGroup.Any("/*any", func(c *gin.Context) {
			apires.RespondServiceUnavailable(c, "HYPERVISOR_MODULE_DEGRADED: Phân hệ Hypervisor hiện đang tạm ngưng hoạt động do lỗi cấu hình hạ tầng.")
		})
	}

	// ------------------------------------------------------------------------
	// 🌐 HẠNG MỤC 5: MAIL VỆ TINH MODULE (TIER-1 DEGRADED-RESILIENT ROUTING)
	// ------------------------------------------------------------------------
	// SRE HA Design Pattern: Nếu module Mail bị degraded/disabled, chúng ta không ẩn
	// hoàn toàn endpoint (gây lỗi 404 khó hiểu cho client), mà tự động đăng ký Fallback Route
	// trả về mã lỗi 503 Service Unavailable chuẩn cấu hình định dạng của apires.
	if m.Mail != nil && m.Mail.IsEnabled() {
		mail.RegisterRoutes(router, m.Mail)
	} else {
		fallbackGroup := router.Group("/api/v1/mail")
		fallbackGroup.Any("/*any", func(c *gin.Context) {
			apires.RespondServiceUnavailable(c, "MAIL_MODULE_DEGRADED: Phân hệ gửi Mail hiện đang tạm ngưng hoạt động do lỗi cấu hình hạ tầng.")
		})
	}
}
