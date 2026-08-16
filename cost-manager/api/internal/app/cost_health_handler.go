package app

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// registerCostHealthRoutes is a flat deployment-health workflow. Its three
// inputs are the exact readiness capabilities; it does not receive App or the
// billing module dependency bag.
func registerCostHealthRoutes(
	router *gin.Engine,
	dbPool *pgxpool.Pool,
	sharedRedis *redis.Client,
	engine *costEngineProcess,
) {
	router.GET("/health/live", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	router.GET("/health/ready", func(c *gin.Context) {
		if !engine.Ready() {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready", "reason": "COST_ENGINE_UNAVAILABLE"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
		defer cancel()
		if err := dbPool.Ping(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready", "reason": "BILLING_POSTGRES_UNAVAILABLE"})
			return
		}
		if err := sharedRedis.Ping(ctx).Err(); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready", "reason": "SHARED_REDIS_UNAVAILABLE"})
			return
		}
		c.Status(http.StatusNoContent)
	})
}
