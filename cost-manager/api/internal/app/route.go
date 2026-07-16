package app

import (
	"cost-manager/api/internal/transport/middleware"
	"github.com/gin-gonic/gin"
)

func RegisterRoutes(router *gin.Engine, m *Module) {
	// Register middlewares
	router.Use(middleware.AccessLog())
	router.Use(middleware.CORS())

	// Register API v1 Group
	apiGroup := router.Group("/api/v1/billing")
	{
		// Wallets
		apiGroup.GET("/wallet", m.WalletHandler.GetWallet)
		apiGroup.POST("/wallet/deposit", m.WalletHandler.Deposit)
		apiGroup.GET("/wallet/:id/transactions", m.WalletHandler.GetTransactions)

		// Prices
		apiGroup.GET("/prices", m.PriceHandler.ListPrices)
		apiGroup.POST("/prices", m.PriceHandler.SavePrice)

		// Zones
		apiGroup.GET("/zones", m.ZoneHandler.ListZones)

		// Plans (Gói cước)
		apiGroup.GET("/plans", m.PlanHandler.ListPlans)
		apiGroup.GET("/plans/:id", m.PlanHandler.GetPlan)
		apiGroup.POST("/plans", m.PlanHandler.CreatePlan)
		apiGroup.PATCH("/plans/:id/status", m.PlanHandler.UpdatePlanStatus)

		// Subscriptions (Đăng ký gói)
		apiGroup.GET("/subscriptions/active", m.SubHandler.GetActiveSubscription)
		apiGroup.POST("/subscriptions", m.SubHandler.Subscribe)
		apiGroup.DELETE("/subscriptions/active", m.SubHandler.CancelSubscription)
	}
}
