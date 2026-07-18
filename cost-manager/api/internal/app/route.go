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
	v1 := router.Group("/api/v1")
	{
		// Route lấy danh sách Plan (Resource SKU Plans)
		v1.GET("/plans", m.PlanHandler.ListPlans)

		// [COMMENT]: Đăng ký thêm route lấy danh sách biểu giá cước lũy tiến (Tiers)
		v1.GET("/tiers", m.TierHandler.ListTiers)
	}
}
