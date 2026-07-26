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
		v1.GET("/plans", middleware.Authorize(m.AuthorizationResolver, "billing:plan:read", false), m.PlanHandler.ListPlans)
		v1.GET("/tiers", middleware.Authorize(m.AuthorizationResolver, "billing:tier:read", false), m.TierHandler.ListTiers)
		v1.GET("/tiers/:service_type/:code", middleware.Authorize(m.AuthorizationResolver, "billing:tier:read", false), m.TierHandler.GetTierDetail)
		v1.PATCH("/critical/tiers/:service_type/:code/metadata", middleware.RequireSessionProof(), middleware.Authorize(m.AuthorizationResolver, "billing:tier:publish", true), m.TierHandler.UpdateTierMetadata)
		v1.POST("/critical/tiers/:service_type/:code/versions", middleware.RequireSessionProof(), middleware.Authorize(m.AuthorizationResolver, "billing:tier:publish", true), m.TierHandler.CreateTierVersion)
		v1.GET("/referrals", middleware.Authorize(m.AuthorizationResolver, "billing:credit:adjust", false), m.AccountHandler.ListReferralCampaigns)
		v1.POST("/critical/referrals", middleware.RequireSessionProof(), middleware.Authorize(m.AuthorizationResolver, "billing:credit:adjust", true), m.AccountHandler.CreateReferralCampaign)
		v1.PATCH("/critical/referrals/:id/status", middleware.RequireSessionProof(), middleware.Authorize(m.AuthorizationResolver, "billing:credit:adjust", true), m.AccountHandler.UpdateReferralCampaignStatus)
	}

	// These are internal owner routes. Browser/SDK calls the neutral
	// `/api/v1/billing/wallet/*` surface; ACR chooses exactly one scope and
	// overwrites :path after verifying the host-bound identity.
	ownerAPI := router.Group("/api/v1")

	personal := ownerAPI.Group("/personal/billing")
	personal.GET("/wallet/summary", m.PersonalPaymentHandler.GetWallet)
	personal.GET("/wallet/onboarding", m.AccountHandler.GetOnboarding)
	personal.POST("/wallet/referral", m.AccountHandler.ReserveReferral)
	personal.POST("/wallet/top-ups", m.PersonalPaymentHandler.CreateTopUp)
	personal.GET("/wallet/top-ups/:id", m.PersonalPaymentHandler.GetTopUp)
	personal.GET("/wallet/estimate/storage", m.TierHandler.EstimateStorage)

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
}
