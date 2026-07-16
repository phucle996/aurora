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
		apiGroup.GET("/wallet", m.WalletHandler.GetWallet)
		apiGroup.POST("/wallet/deposit", m.WalletHandler.Deposit)
		apiGroup.GET("/wallet/:id/transactions", m.WalletHandler.GetTransactions)
		apiGroup.GET("/prices", m.PriceHandler.ListPrices)
		apiGroup.POST("/prices", m.PriceHandler.SavePrice)
		apiGroup.GET("/zones", m.ZoneHandler.ListZones)
	}
}
