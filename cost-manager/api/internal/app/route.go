package app

import (
	"cost-manager/api/internal/transport/http/handler"
	"cost-manager/api/internal/transport/middleware"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes thiết lập toàn bộ cây định tuyến phẳng (Flat Routing Tree) cho Cost Manager API.
// Toàn bộ API path được viết tường minh trực tiếp trên từng route, không lồng router.Group để dễ tra cứu và bảo trì.
func RegisterRoutes(router *gin.Engine, m *Module, health *handler.HealthHandler) {
	// ========================================================================
	// 0. HEALTH CHECK PROBES (/health/...)
	// ========================================================================
	if health != nil {
		router.GET("/health/live", health.Liveness)
		router.GET("/health/startup", health.Startup)
		router.GET("/health/ready", health.Readiness)
	}

	// ========================================================================
	// 1. GLOBAL MIDDLEWARES
	// ========================================================================
	router.Use(middleware.AccessLog())
	router.Use(middleware.ContextInjector())
	// [LƯU Ý]: CORS do Envoy Reverse Proxy / Ingress Gateway enforce; backend không phát wildcard.

	// ========================================================================
	// 2. PAYMENT PROVIDER WEBHOOKS (/api/v1/billing/webhooks/...)
	// ========================================================================
	// Webhook từ cổng thanh toán bên thứ 3 (MoMo, VNPay, Stripe,...).
	// Mỗi webhook handler tự xác thực chữ ký thô (raw signature) và ghi nhận giao dịch vào đúng nhánh sổ cái.
	router.POST("/api/v1/billing/webhooks/personal/payment-settled", m.PersonalPaymentHandler.ApplySettlement)
	router.POST("/api/v1/billing/webhooks/tenant/payment-settled", m.TenantPaymentHandler.ApplySettlement)

	// ========================================================================
	// 3. CATALOG & PRICING MANAGEMENT API (/api/v1/billing/...)
	// ========================================================================
	// --- 3.1. Tra cứu danh mục bảng giá & hệ số khu vực (Read Catalog) ---
	router.GET(
		"/api/v1/billing/pricing-schedules",
		m.PersonalAuthorizationMiddleware.Authorize("billing:pricing_schedule:read", false),
		m.PricingScheduleHandler.ListPricingSchedules,
	)
	router.GET(
		"/api/v1/billing/pricing-schedules/:code",
		m.PersonalAuthorizationMiddleware.Authorize("billing:pricing_schedule:read", false),
		m.PricingScheduleHandler.GetPricingScheduleDetail,
	)
	router.GET(
		"/api/v1/billing/pricing-schedules/:code/rate-state",
		m.PersonalAuthorizationMiddleware.Authorize("billing:pricing_schedule:read", false),
		m.PricingScheduleRateStateHandler.GetPricingScheduleRateState,
	)
	router.GET(
		"/api/v1/billing/storage/zone-price-adjustments",
		m.PersonalAuthorizationMiddleware.Authorize("billing:pricing_schedule:read", false),
		m.StoragePricingHandler.ListZonePriceAdjustments,
	)
	router.GET(
		"/api/v1/billing/mail/zone-price-adjustments",
		m.PersonalAuthorizationMiddleware.Authorize("billing:pricing_schedule:read", false),
		m.MailPricingHandler.ListZonePriceAdjustments,
	)
	router.GET(
		"/api/v1/billing/hypervisor/zone-price-adjustments",
		m.PersonalAuthorizationMiddleware.Authorize("billing:pricing_schedule:read", false),
		m.HypervisorPricingHandler.ListZonePriceAdjustments,
	)
	router.GET(
		"/api/v1/billing/hypervisor/resource-plans",
		m.PersonalAuthorizationMiddleware.Authorize("billing:pricing_schedule:read", false),
		m.HypervisorResourcePlanHandler.ListAdmin,
	)
	router.GET("/api/v1/billing/hypervisor/resource-plans/:plan_id/revisions",
		m.PersonalAuthorizationMiddleware.Authorize("billing:pricing_schedule:read", false),
		m.HypervisorResourcePlanHandler.ListRevisions,
	)
	router.GET(
		"/api/v1/billing/referrals",
		m.PersonalAuthorizationMiddleware.Authorize("billing:credit:adjust", false),
		m.PersonalAccountHandler.ListReferralCampaigns,
	)

	// --- 3.2. Nghiệp vụ Quản trị viên nhạy cảm (Critical Mutations: Yêu cầu Session Proof + Quyền Cấp 3) ---
	router.PATCH(
		"/api/v1/billing/critical/pricing-schedules/:code/metadata",
		middleware.RequireSessionProof(),
		m.PersonalAuthorizationMiddleware.Authorize("billing:pricing_schedule:publish", true),
		m.PricingScheduleHandler.UpdatePricingScheduleMetadata,
	)
	router.POST(
		"/api/v1/billing/critical/storage/pricing-schedules/:code/versions",
		middleware.RequireSessionProof(),
		m.PersonalAuthorizationMiddleware.Authorize("billing:pricing_schedule:publish", true),
		m.StoragePricingHandler.CreateBasePriceVersion,
	)
	router.POST(
		"/api/v1/billing/critical/hypervisor/pricing-schedules/:code/versions",
		middleware.RequireSessionProof(),
		m.PersonalAuthorizationMiddleware.Authorize("billing:pricing_schedule:publish", true),
		m.HypervisorPricingHandler.CreateBasePriceVersion,
	)
	router.POST(
		"/api/v1/billing/critical/hypervisor/resource-plans",
		middleware.RequireSessionProof(),
		m.PersonalAuthorizationMiddleware.Authorize("billing:pricing_schedule:publish", true),
		m.HypervisorResourcePlanHandler.Create,
	)
	router.POST(
		"/api/v1/billing/critical/hypervisor/resource-plans/:plan_id/revisions",
		middleware.RequireSessionProof(),
		m.PersonalAuthorizationMiddleware.Authorize("billing:pricing_schedule:publish", true),
		m.HypervisorResourcePlanHandler.PublishRevision,
	)
	router.POST(
		"/api/v1/billing/critical/mail/pricing-schedules/:code/versions",
		middleware.RequireSessionProof(),
		m.PersonalAuthorizationMiddleware.Authorize("billing:pricing_schedule:publish", true),
		m.MailPricingHandler.CreateBasePriceVersion,
	)
	router.POST(
		"/api/v1/billing/critical/storage/zone-price-adjustments/versions",
		middleware.RequireSessionProof(),
		m.PersonalAuthorizationMiddleware.Authorize("billing:pricing_schedule:publish", true),
		m.StoragePricingHandler.CreateZonePriceAdjustment,
	)
	router.POST(
		"/api/v1/billing/critical/hypervisor/zone-price-adjustments/versions",
		middleware.RequireSessionProof(),
		m.PersonalAuthorizationMiddleware.Authorize("billing:pricing_schedule:publish", true),
		m.HypervisorPricingHandler.CreateZonePriceAdjustment,
	)
	router.POST(
		"/api/v1/billing/critical/mail/zone-price-adjustments/versions",
		middleware.RequireSessionProof(),
		m.PersonalAuthorizationMiddleware.Authorize("billing:pricing_schedule:publish", true),
		m.MailPricingHandler.CreateZonePriceAdjustment,
	)
	router.POST(
		"/api/v1/billing/critical/referrals",
		middleware.RequireSessionProof(),
		m.PersonalAuthorizationMiddleware.Authorize("billing:credit:adjust", true),
		m.PersonalAccountHandler.CreateReferralCampaign,
	)
	router.PATCH(
		"/api/v1/billing/critical/referrals/:id/status",
		middleware.RequireSessionProof(),
		m.PersonalAuthorizationMiddleware.Authorize("billing:credit:adjust", true),
		m.PersonalAccountHandler.UpdateReferralCampaignStatus,
	)

	// ========================================================================
	// 4. PERSONAL BILLING API (/api/v1/personal/billing/...)
	// ========================================================================
	// Các endpoint dành cho tài khoản cá nhân, được ACR điều hướng từ route `/api/v1/billing/wallet/*`
	// Quản lý số dư, nạp tiền và chương trình Onboarding / Referral
	router.GET("/api/v1/personal/billing/wallet/summary", m.PersonalPaymentHandler.GetWallet)
	router.GET("/api/v1/personal/billing/wallet/onboarding", m.PersonalAccountHandler.GetOnboarding)
	router.POST("/api/v1/personal/billing/wallet/referral", m.PersonalAccountHandler.ReserveReferral)
	router.POST("/api/v1/personal/billing/wallet/top-ups", m.PersonalPaymentHandler.CreateTopUp)
	router.GET("/api/v1/personal/billing/wallet/top-ups/:id", m.PersonalPaymentHandler.GetTopUp)

	// Dự toán cước phí thời gian thực (Real-time Estimation)
	router.GET("/api/v1/personal/billing/wallet/estimate/storage", m.StoragePricingHandler.Estimate)
	router.GET("/api/v1/personal/billing/wallet/estimate/hypervisor", m.HypervisorPricingHandler.Estimate)
	router.GET("/api/v1/personal/billing/hypervisor/resource-plans", m.HypervisorResourcePlanHandler.ListEffective)
	router.GET("/api/v1/personal/billing/wallet/hypervisor/resource-plans", m.HypervisorResourcePlanHandler.ListEffective)
	router.GET("/api/v1/personal/billing/wallet/estimate/mail", m.MailPricingHandler.Estimate)

	// ========================================================================
	// 5. TENANT BILLING API (/api/v1/tenant/billing/...)
	// ========================================================================
	// Các endpoint dành cho tổ chức / doanh nghiệp, yêu cầu kiểm tra quyền theo Workspace/Tenant
	router.GET(
		"/api/v1/tenant/billing/wallet/summary",
		m.TenantAuthorizationMiddleware.Authorize("billing:wallet:read", false),
		m.TenantPaymentHandler.GetWallet,
	)
	router.POST(
		"/api/v1/tenant/billing/wallet/top-ups",
		m.TenantAuthorizationMiddleware.Authorize("billing:wallet:top_up", true),
		m.TenantPaymentHandler.CreateTopUp,
	)
	router.GET(
		"/api/v1/tenant/billing/wallet/top-ups/:id",
		m.TenantAuthorizationMiddleware.Authorize("billing:wallet:read", false),
		m.TenantPaymentHandler.GetTopUp,
	)
	router.GET(
		"/api/v1/tenant/billing/wallet/estimate/mail",
		m.TenantAuthorizationMiddleware.Authorize("billing:wallet:read", false),
		m.MailPricingHandler.Estimate,
	)
	router.GET(
		"/api/v1/tenant/billing/hypervisor/resource-plans",
		m.TenantAuthorizationMiddleware.Authorize("billing:wallet:read", false),
		m.HypervisorResourcePlanHandler.ListEffective,
	)
	router.GET(
		"/api/v1/tenant/billing/wallet/hypervisor/resource-plans",
		m.TenantAuthorizationMiddleware.Authorize("billing:wallet:read", false),
		m.HypervisorResourcePlanHandler.ListEffective,
	)
}
