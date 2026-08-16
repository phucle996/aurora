package app

import (
	"cost-manager/api/internal/transport/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(router *gin.Engine, m *Module) {
	// Đăng ký các global middlewares
	router.Use(middleware.AccessLog())
	router.Use(middleware.ContextInjector())
	// [COMMENT]: CORS do Envoy vhost enforce; backend không phát wildcard + credentials mâu thuẫn.

	// Provider webhooks are owner-specific contracts. Each handler authenticates
	// the exact raw body and writes only its own payment/ledger branch.
	billing := router.Group("/api/v1/billing")
	billing.POST("/webhooks/personal/payment-settled", m.PersonalPaymentHandler.ApplySettlement)
	billing.POST("/webhooks/tenant/payment-settled", m.TenantPaymentHandler.ApplySettlement)

	// Đăng ký các endpoints phiên bản v1
	v1 := billing.Group("")
	{
		v1.GET("/pricing-schedules", middleware.Authorize(m.AuthorizationResolver, "billing:pricing_schedule:read", false), m.PricingScheduleHandler.ListPricingSchedules)
		v1.GET("/pricing-schedules/:code", middleware.Authorize(m.AuthorizationResolver, "billing:pricing_schedule:read", false), m.PricingScheduleHandler.GetPricingScheduleDetail)
		v1.GET("/mail/zone-price-adjustments", middleware.Authorize(m.AuthorizationResolver, "billing:pricing_schedule:read", false), m.MailPricingHandler.ListZonePriceAdjustments)
		v1.PATCH("/critical/pricing-schedules/:code/metadata", middleware.RequireSessionProof(), middleware.Authorize(m.AuthorizationResolver, "billing:pricing_schedule:publish", true), m.PricingScheduleHandler.UpdatePricingScheduleMetadata)
		v1.POST("/critical/pricing-schedules/:code/versions", middleware.RequireSessionProof(), middleware.Authorize(m.AuthorizationResolver, "billing:pricing_schedule:publish", true), m.PricingScheduleHandler.CreatePricingScheduleVersion)
		v1.POST("/critical/storage/zone-price-adjustments/versions", middleware.RequireSessionProof(), middleware.Authorize(m.AuthorizationResolver, "billing:pricing_schedule:publish", true), m.PricingScheduleHandler.CreateStorageZonePriceAdjustment)
		v1.POST("/critical/hypervisor/zone-price-adjustments/versions", middleware.RequireSessionProof(), middleware.Authorize(m.AuthorizationResolver, "billing:pricing_schedule:publish", true), m.HypervisorPricingHandler.CreateZonePriceAdjustment)
		v1.POST("/critical/mail/zone-price-adjustments/versions", middleware.RequireSessionProof(), middleware.Authorize(m.AuthorizationResolver, "billing:pricing_schedule:publish", true), m.MailPricingHandler.CreateZonePriceAdjustment)
		v1.GET("/referrals", middleware.Authorize(m.AuthorizationResolver, "billing:credit:adjust", false), m.PersonalAccountHandler.ListReferralCampaigns)
		v1.POST("/critical/referrals", middleware.RequireSessionProof(), middleware.Authorize(m.AuthorizationResolver, "billing:credit:adjust", true), m.PersonalAccountHandler.CreateReferralCampaign)
		v1.PATCH("/critical/referrals/:id/status", middleware.RequireSessionProof(), middleware.Authorize(m.AuthorizationResolver, "billing:credit:adjust", true), m.PersonalAccountHandler.UpdateReferralCampaignStatus)
	}

	// These are internal owner routes. Browser/SDK calls the neutral
	// `/api/v1/billing/wallet/*` surface; ACR chooses exactly one scope and
	// overwrites :path after verifying the host-bound identity.
	ownerAPI := router.Group("/api/v1")

	personal := ownerAPI.Group("/personal/billing")
	personal.GET("/wallet/summary", m.PersonalPaymentHandler.GetWallet)
	personal.GET("/wallet/onboarding", m.PersonalAccountHandler.GetOnboarding)
	personal.POST("/wallet/referral", m.PersonalAccountHandler.ReserveReferral)
	personal.POST("/wallet/top-ups", m.PersonalPaymentHandler.CreateTopUp)
	personal.GET("/wallet/top-ups/:id", m.PersonalPaymentHandler.GetTopUp)
	personal.GET("/wallet/estimate/storage", m.PricingScheduleHandler.EstimateStorage)
	personal.GET("/wallet/estimate/hypervisor", m.HypervisorPricingHandler.Estimate)
	personal.GET("/wallet/estimate/mail", m.MailPricingHandler.Estimate)

	tenant := ownerAPI.Group("/tenant/billing")
	tenant.GET(
		"/wallet/summary",
		middleware.AuthorizeTenant(m.AuthorizationResolver, "billing:wallet:read", false),
		m.TenantPaymentHandler.GetWallet,
	)
	tenant.POST(
		"/wallet/top-ups",
		middleware.AuthorizeTenant(m.AuthorizationResolver, "billing:wallet:top_up", true),
		m.TenantPaymentHandler.CreateTopUp,
	)
	tenant.GET(
		"/wallet/top-ups/:id",
		middleware.AuthorizeTenant(m.AuthorizationResolver, "billing:wallet:read", false),
		m.TenantPaymentHandler.GetTopUp,
	)
	tenant.GET(
		"/wallet/estimate/mail",
		middleware.AuthorizeTenant(m.AuthorizationResolver, "billing:wallet:read", false),
		m.MailPricingHandler.Estimate,
	)
}
