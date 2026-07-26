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

	// Đăng ký các endpoints phiên bản v1
	v1 := router.Group("/api/v1/billing", middleware.RequireIdentity())
	{
		v1.GET("/plans", middleware.Authorize(m.AuthorizationResolver, "billing:plan:read", false), m.PlanHandler.ListPlans)
		v1.GET("/tiers", middleware.Authorize(m.AuthorizationResolver, "billing:tier:read", false), m.TierHandler.ListTiers)
		v1.GET("/tiers/:service_type/:code", middleware.Authorize(m.AuthorizationResolver, "billing:tier:read", false), m.TierHandler.GetTierDetail)
		v1.PATCH("/critical/tiers/:service_type/:code/metadata", middleware.RequireSessionProof(), middleware.Authorize(m.AuthorizationResolver, "billing:tier:publish", true), m.TierHandler.UpdateTierMetadata)
		v1.POST("/critical/tiers/:service_type/:code/versions", middleware.RequireSessionProof(), middleware.Authorize(m.AuthorizationResolver, "billing:tier:publish", true), m.TierHandler.CreateTierVersion)
		v1.POST("/subscriptions/free-tier/personal", middleware.Authorize(m.AuthorizationResolver, "billing:subscription:write", false), m.AccountHandler.ActivatePersonalFreeTier)
	}

	// Self-scoped Billing reads stay under the Billing API branch so Envoy can route the whole
	// `/api/v1/billing/me/` subtree without an exact-route change for every new read endpoint.
	// They intentionally do not use the operator Billing permission/alias middleware.
	billingMe := v1.Group("/me")
	billingMe.GET("/wallet/summary", m.AccountHandler.GetPersonalWalletSummary)
	billingMe.GET("/estimate/storage", m.TierHandler.EstimateStorage)
}
