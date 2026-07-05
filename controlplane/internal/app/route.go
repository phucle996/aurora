// ============================================================================
// 🗺️ ARCHITECTURAL COMPONENT: GLOBAL ROUTE ORCHESTRATOR
// ============================================================================
// CONTRACT: Đóng vai trò là điểm định tuyến trung tâm API Gateway Facade cho tất cả
// các phân hệ chạy trong Control Plane.
//
// SOT: Xác định toàn cảnh cấu trúc URL định tuyến, thứ tự ưu tiên nạp route và cấu
// hình phân nhánh API toàn cục của Control Plane.
//
// BOUNDARY: Phân tầng các phân hệ theo High Availability:
//   - Tier 1 Critical: Các module cốt lõi Health, IAM, Core. Bắt buộc chạy ổn định.
//   - Tier 2 Non-critical: Các module vệ tinh Hypervisor, Mail hỗ trợ Graceful Degradation.
// ============================================================================

package app

import (
	"controlplane/internal/hierarchy"
	"controlplane/internal/hypervisor"
	"controlplane/internal/iam"
	"controlplane/internal/mail"
	"controlplane/internal/storage"
	"controlplane/pkg/apires"

	"github.com/gin-gonic/gin"
)

// NewGlobalRoutes thực hiện lập đồ thị và đăng ký toàn bộ định tuyến cho các phân hệ của Control Plane.
func NewGlobalRoutes(router *gin.Engine, m *Modules) {
	// CONTRACT: Yêu cầu router khác nil. Các module con m.Health, m.IAM, m.Core được đảm bảo không nil
	// nhờ cơ chế kiểm tra dependency ở cấp độ bootstrap NewGlobalModules.
	if router == nil {
		return
	}

	// ========================================================================
	// TIER 1 CRITICAL SERVICES
	// ========================================================================

	// 1. Kubernetes Probes & System Health với Latency dưới 1ms
	router.GET("/api/v1/health/liveness", m.Health.Liveness)
	router.GET("/api/v1/health/readiness", m.Health.Readiness)
	router.GET("/api/v1/health/startup", m.Health.Startup)

	// 2. IAM Module Routing cho Authentication và Authorization
	iam.RegisterRoutes(router, m.IAM)

	// 3. Core Module Routing cho các nghiệp vụ hệ thống
	core.RegisterRoutes(router, m.Core)

	// ========================================================================
	// TIER 2 NON-CRITICAL SERVICES
	// ========================================================================

	// 1. Hypervisor Module hỗ trợ Fallback Route trả về HTTP 503 khi disabled hoặc degraded
	if m.Hypervisor != nil && m.Hypervisor.IsEnabled() {
		hypervisor.RegisterRoutes(router, m.Hypervisor)
	} else {
		fallbackGroup := router.Group("/api/v1/hypervisor")
		fallbackGroup.Any("/*any", func(c *gin.Context) {
			apires.RespondServiceUnavailable(c, "HYPERVISOR_MODULE_DEGRADED: Phân hệ Hypervisor hiện đang tạm ngưng hoạt động do lỗi cấu hình hạ tầng.")
		})
	}

	// 2. Mail Module hỗ trợ Fallback Route trả về HTTP 503 khi disabled hoặc degraded
	if m.Mail != nil && m.Mail.IsEnabled() {
		mail.RegisterRoutes(router, m.Mail)
	} else {
		fallbackGroup := router.Group("/api/v1/mail")
		fallbackGroup.Any("/*any", func(c *gin.Context) {
			apires.RespondServiceUnavailable(c, "MAIL_MODULE_DEGRADED: Phân hệ gửi Mail hiện đang tạm ngưng hoạt động do lỗi cấu hình hạ tầng.")
		})
	}

	// [COMMENT]: 3. Storage Module (Tier 2) hỗ trợ Fallback Route trả về HTTP 503 khi disabled hoặc degraded
	if m.Storage != nil && m.Storage.IsEnabled() {
		storage.RegisterRoutes(router, m.Storage)
	} else {
		fallbackGroup := router.Group("/api/v1/storage")
		fallbackGroup.Any("/*any", func(c *gin.Context) {
			apires.RespondServiceUnavailable(c, "STORAGE_MODULE_DEGRADED: Phân hệ Object Storage hiện đang tạm ngưng hoạt động do lỗi cấu hình hạ tầng.")
		})
	}
}
