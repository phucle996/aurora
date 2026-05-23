package app

import (
	"controlplane/internal/core"
	"controlplane/internal/iam"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(router *gin.Engine, m *Modules) {
	if router == nil {
		return
	}

	if m != nil && m.Health != nil {
		router.GET("/api/v1/health/liveness", m.Health.Liveness)
		router.GET("/api/v1/health/readiness", m.Health.Readiness)
		router.GET("/api/v1/health/startup", m.Health.Startup)
	}

	if m != nil && m.IAM != nil {
		iam.RegisterRoutes(router, m.IAM)
	}

	if m != nil && m.Core != nil {
		core.RegisterRoutes(router, m.Core)
	}

}
