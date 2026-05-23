package handler

import (
	"context"
	"sync/atomic"
	"time"

	response "controlplane/pkg/apires"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type HealthHandler struct {
	db               *pgxpool.Pool
	redis            *redis.Client
	ready            atomic.Bool
	timeDriftSeconds atomic.Uint64
	timeDriftState   atomic.Value
}

func NewHealthHandler(db *pgxpool.Pool, redis *redis.Client) *HealthHandler {
	h := &HealthHandler{db: db, redis: redis}
	h.timeDriftState.Store("unknown")
	return h
}

func (h *HealthHandler) SetTimeDrift(seconds float64, state string) {
	h.timeDriftSeconds.Store(uint64(seconds * 1_000_000_000))
	h.timeDriftState.Store(state)
}

func (h *HealthHandler) MarkReady()    { h.ready.Store(true) }
func (h *HealthHandler) MarkNotReady() { h.ready.Store(false) }

func (h *HealthHandler) Liveness(c *gin.Context) {
	response.RespondSuccess(c, gin.H{"status": "ok"}, "alive")
}

func (h *HealthHandler) Startup(c *gin.Context) {
	if !h.ready.Load() {
		response.RespondServiceUnavailable(c, "app still starting")
		return
	}
	response.RespondSuccess(c, gin.H{"status": "ok"}, "started")
}

func (h *HealthHandler) Readiness(c *gin.Context) {
	if !h.ready.Load() {
		response.RespondServiceUnavailable(c, "app not ready")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	status := gin.H{"postgres": "skipped", "redis": "skipped", "time_sync_state": h.timeDriftState.Load(), "time_drift_seconds": float64(h.timeDriftSeconds.Load()) / 1_000_000_000}
	var errs []string

	if h.db != nil {
		if err := h.db.Ping(ctx); err != nil {
			status["postgres"] = "unhealthy"
			errs = append(errs, "postgres: "+err.Error())
		} else {
			status["postgres"] = "ok"
		}
	}

	if h.redis != nil {
		if err := h.redis.Ping(ctx).Err(); err != nil {
			status["redis"] = "unhealthy"
			errs = append(errs, "redis: "+err.Error())
		} else {
			status["redis"] = "ok"
		}
	}

	if len(errs) > 0 {
		msg := errs[0]
		for _, e := range errs[1:] {
			msg += "; " + e
		}
		response.RespondServiceUnavailable(c, "readiness failed: "+msg)
		return
	}

	response.RespondSuccess(c, status, "ready")
}
