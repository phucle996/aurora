package handler

import (
	"context"
	"strings"
	"time"

	"cost-manager/api/pkg/apires"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// EngineReadinessChecker định nghĩa interface kiểm tra trạng thái sẵn sàng của Cost Engine subprocess.
type EngineReadinessChecker interface {
	Ready() bool
}

// HealthHandler quản lý bộ endpoints kiểm tra sức khỏe đầy đủ cho Kubernetes (Liveness, Startup, Readiness Probes).
type HealthHandler struct {
	dbPool      *pgxpool.Pool
	sharedRedis *redis.Client
	engine      EngineReadinessChecker
}

// NewHealthHandler khởi tạo một instance mới của HealthHandler.
func NewHealthHandler(
	dbPool *pgxpool.Pool,
	sharedRedis *redis.Client,
	engine EngineReadinessChecker,
) *HealthHandler {
	return &HealthHandler{
		dbPool:      dbPool,
		sharedRedis: sharedRedis,
		engine:      engine,
	}
}

// Liveness xử lý Kubernetes Liveness Probe (/health/live):
// - Mục đích: Xác nhận process Go runtime và HTTP server vẫn đang chạy, không bị deadlock.
// - Nguyên tắc an toàn: Không ping DB/Redis tại Liveness để tránh K8s restart Pod hàng loạt khi DB nghẽn tạm thời (Boot Storm).
func (h *HealthHandler) Liveness(c *gin.Context) {
	apires.RespondSuccess(c, gin.H{"status": "alive"}, "service is alive")
}

// Startup xử lý Kubernetes Startup Probe (/health/startup):
// - Mục đích: Dành riêng cho K8s kiểm tra quá trình bootstrap ban đầu (khi load Vault secrets, migration, và spawn Rust Engine).
// - Khi chưa khởi động xong Cost Engine subprocess, trả về 503 để K8s tiếp tục chờ mà không restart Pod.
func (h *HealthHandler) Startup(c *gin.Context) {
	if h.engine != nil && !h.engine.Ready() {
		apires.RespondServiceUnavailable(c, "cost engine subprocess is starting")
		return
	}
	apires.RespondSuccess(c, gin.H{"status": "started"}, "service bootstrap completed")
}

// Readiness xử lý Kubernetes Readiness Probe (/health/ready):
// - Mục đích: Xác nhận Pod đã sẵn sàng tiếp nhận traffic định tuyến từ Ingress/Envoy Gateway.
// - Kiểm tra đồng thời: Cost Engine subprocess, PostgreSQL Database Pool, và Shared Redis.
func (h *HealthHandler) Readiness(c *gin.Context) {
	if h.engine != nil && !h.engine.Ready() {
		apires.RespondServiceUnavailable(c, "cost engine subprocess is unavailable")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	status := gin.H{
		"cost_engine": "ready",
		"postgres":    "skipped",
		"redis":       "skipped",
	}
	var errs []string

	if h.dbPool != nil {
		if err := h.dbPool.Ping(ctx); err != nil {
			status["postgres"] = "unhealthy"
			errs = append(errs, "postgres: "+err.Error())
		} else {
			status["postgres"] = "ok"
		}
	}

	if h.sharedRedis != nil {
		if err := h.sharedRedis.Ping(ctx).Err(); err != nil {
			status["redis"] = "unhealthy"
			errs = append(errs, "redis: "+err.Error())
		} else {
			status["redis"] = "ok"
		}
	}

	if len(errs) > 0 {
		apires.RespondServiceUnavailable(c, "readiness failed: "+strings.Join(errs, "; "))
		return
	}

	apires.RespondSuccess(c, status, "service is ready")
}
