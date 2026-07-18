package app

import (
	"cost-manager/api/internal/transport/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(router *gin.Engine, m *Module) {
	// Đăng ký các global middlewares
	router.Use(middleware.AccessLog())
	router.Use(middleware.CORS())

	// Đăng ký các endpoints phiên bản v1
	v1 := router.Group("/api/v1/billing")
	{
		v1.GET("/plans", m.PlanHandler.ListPlans)
		v1.GET("/tiers", m.TierHandler.ListTiers)
		v1.GET("/tiers/:service_type/:code", m.TierHandler.GetTierDetail)
		v1.PATCH("/tiers/:service_type/:code/metadata", m.TierHandler.UpdateTierMetadata)
		v1.POST("/tiers/:service_type/:code/versions", m.TierHandler.CreateTierVersion)
		v1.POST("/subscriptions/free-tier/personal", m.AccountHandler.ActivatePersonalFreeTier)
	}
}
