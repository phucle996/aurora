package app

import (
	"cost-manager/api/internal/transport/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(router *gin.Engine, m *Module) {
	// Register middlewares
	router.Use(middleware.AccessLog())
	router.Use(middleware.CORS())

}
