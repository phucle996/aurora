package app

import (
	"context"
	"controlplane/infra/psql"
	redisinfra "controlplane/infra/redis"
	"controlplane/internal/app/bootstrap"
	"controlplane/internal/config"
	"controlplane/internal/http/middleware"
	"controlplane/internal/observability"
	"controlplane/internal/policyengine"
	otelPolicy "controlplane/internal/policyengine/policies/otel"
	"controlplane/internal/security"
	"controlplane/internal/security/ratelimit"
	"controlplane/pkg/logger"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

// App là root runtime container của controlplane process.
//
// Trách nhiệm:
// - giữ lifecycle context và hàm cancel,
// - giữ references đến infra runtime (DB/Redis/OTel/HTTP/gRPC),
// - start/stop toàn bộ server theo đúng thứ tự.
//
// Lưu ý: App.Stop() phải idempotent và nil-safe để có thể gọi
// cả trong nhánh bootstrap error path lẫn shutdown bình thường.
type App struct {
	ctx        context.Context
	cancel     context.CancelFunc
	cfg        *config.Config
	modules    *Modules
	otel       *observability.OTel
	prom       *observability.Prometheus
	httpServer *http.Server
	grpc       *bootstrap.GRPC
	psql       *pgxpool.Pool
	rds        *goredis.Client
	ready      bool
}

// NewApplication khởi tạo toàn bộ runtime dependency và trả về App đã sẵn sàng Start().
//
// Startup order (fail-fast):
// 1) validate + set runtime master key
// 2) init Postgres + Redis
// 3) run migrations
// 4) init observability (OTel + Prometheus)
// 5) init HTTP engine + middlewares
// 6) init modules + chạy module bootstraps
// 7) init gRPC server
// 8) register routes + build HTTP server
//
// Nếu bất kỳ bước nào lỗi, hàm gọi app.Stop() để cleanup thống nhất
// (single cleanup path) rồi trả lỗi ra ngoài.
func NewApplication(cfg *config.Config) (*App, error) {
	if cfg == nil {
		return nil, fmt.Errorf("bootstrap: config is required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	app := &App{ctx: ctx, cancel: cancel, cfg: cfg}

	// Security bootstrap: runtime master key là bắt buộc cho secret encryption/decryption.
	runtimeMasterKey, err := resolveRuntimeMasterKey(cfg.Security.RuntimeMasterKey)
	if err != nil {
		app.Stop()
		return nil, err
	}
	security.SetRuntimeMasterKey(runtimeMasterKey)

	// Infrastructure bootstrap: Postgres.
	db, err := psql.NewPostgres(ctx, &cfg.Psql)
	if err != nil {
		app.Stop()
		return nil, fmt.Errorf("bootstrap: psql init failed: %w", err)
	}
	app.psql = db

	// Infrastructure bootstrap: Redis.
	rds, err := redisinfra.NewRedis(ctx, &cfg.Redis)
	if err != nil {
		app.Stop()
		return nil, fmt.Errorf("bootstrap: redis init failed: %w", err)
	}
	app.rds = rds
	if rds == nil {
		app.Stop()
		return nil, fmt.Errorf("bootstrap: redis client is required")
	}

	// Schema bootstrap phải chạy trước khi modules bắt đầu dùng DB.
	if err := bootstrap.RunMigrations(ctx, db, cfg); err != nil {
		app.Stop()
		return nil, err
	}

	// Early boot Policy Engine (System Infrastructure)
	policyModule, err := policyengine.New(cfg, rds)
	if err != nil {
		app.Stop()
		return nil, fmt.Errorf("bootstrap: policy engine init failed: %w", err)
	}
	defer func() {
		if err != nil {
			policyModule.Stop()
		}
	}()

	policySet, err := policyModule.EngineService.Current(ctx)
	if err != nil {
		app.Stop()
		return nil, fmt.Errorf("bootstrap: failed to get active policy set: %w", err)
	}
	otelCfg := &policySet.Runtime.OTel

	// Observability bootstrap: OTel trước, Prometheus sau.
	otelObs, err := observability.InitOTel(ctx, otelCfg, "aurora-controlplane")
	if err != nil {
		app.Stop()
		return nil, fmt.Errorf("bootstrap: otel init failed: %w", err)
	}
	app.otel = otelObs

	// Hook OTel updates dynamically to the Policy Engine!
	policyModule.EngineService.RegisterOTelHook(func(newOTelCfg *otelPolicy.CompiledPolicy) {
		if err := otelObs.Update(context.Background(), newOTelCfg, "aurora-controlplane"); err != nil {
			logger.SysError("app", fmt.Sprintf("failed to hot-swap OTel config: %v", err))
		}
	})

	promObs, err := observability.InitPrometheus("aurora_controlplane")
	if err != nil {
		app.Stop()
		return nil, fmt.Errorf("bootstrap: prometheus init failed: %w", err)
	}
	app.prom = promObs

	ratelimiter := ratelimit.NewBucket(rds)
	ratelimiter.SetFailOpen(false)

	// HTTP engine bootstrap.
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	if err := engine.SetTrustedProxies(cfg.App.TrustedProxies); err != nil {
		app.Stop()
		return nil, fmt.Errorf("bootstrap: set trusted proxies failed: %w", err)
	}

	// Global middlewares được áp dụng trước khi register routes.
	engine.Use(
		gin.Recovery(),
		middleware.RequestID(),
		middleware.OTelTraceContext(otelObs),
		middleware.PrometheusHTTPMetrics(promObs),
		middleware.CORS(cfg.App.AllowedOrigins),
		middleware.CookieOriginGuard(cfg.App.AllowedOrigins),
		middleware.RateLimitPreAuth(ratelimiter, "global_preauth", 1200, 1200, time.Minute),
		middleware.AccessLog(),
	)
	engine.GET("/metrics", middleware.PrometheusMetricsEndpoint(promObs))

	// Module bootstrap.
	modules, err := NewGlobalModules(cfg, db, rds, ratelimiter, policyModule)
	if err != nil {
		// Fail-fast: module graph ảnh hưởng cross-module (core security provider,
		// IAM wiring, middleware auth). Không degrade ở app runtime bootstrap.
		app.Stop()
		return nil, err
	}
	app.modules = modules

	// Chạy module-level bootstrap hooks (bao gồm IAM bootstrap contract).
	bootstrapCtx, bootstrapCancel := context.WithTimeout(app.ctx, 20*time.Second)
	defer bootstrapCancel()
	if err := RunModuleBootstraps(bootstrapCtx, modules); err != nil {
		app.Stop()
		return nil, err
	}

	// Transport bootstrap: gRPC server.
	g, err := bootstrap.InitGRPCServer(&cfg.GRPC)
	if err != nil {
		app.Stop()
		return nil, err
	}
	app.grpc = g

	// Register tất cả HTTP routes sau khi modules đã sẵn sàng.
	RegisterRoutes(engine, modules)

	// HTTP server runtime config.
	httpSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.App.HTTPPort),
		Handler:           engine,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	app.httpServer = httpSrv
	return app, nil
}

// resolveRuntimeMasterKey decode và validate SECURITY_RUNTIME_MASTER_KEY.
//
// Yêu cầu:
// - input phải là base64/base64raw hợp lệ,
// - output phải đúng 32 bytes (AES-256 key length).
func resolveRuntimeMasterKey(encoded string) ([]byte, error) {
	value := strings.TrimSpace(encoded)
	if value == "" {
		return nil, fmt.Errorf("bootstrap: SECURITY_RUNTIME_MASTER_KEY is required")
	}

	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(value)
		if err != nil {
			return nil, fmt.Errorf("bootstrap: SECURITY_RUNTIME_MASTER_KEY must be valid base64")
		}
	}
	if len(decoded) != 32 {
		return nil, fmt.Errorf("bootstrap: SECURITY_RUNTIME_MASTER_KEY must decode to exactly 32 bytes")
	}
	return decoded, nil
}

// Start chạy runtime servers (gRPC + HTTP) theo mode non-blocking.
//
// Lưu ý:
// - Start không tự retry nếu ListenAndServe lỗi.
// - Lỗi runtime của server được log ở goroutine; caller quản lý lifecycle tổng thể.
func (a *App) Start() error {
	if a.grpc != nil {
		go func() {
			if err := a.grpc.Start(); err != nil {
				logger.SysError("app", fmt.Sprintf("gRPC server stopped: %v", err))
			}
		}()
	}

	go func() {
		if err := a.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.SysError("app", fmt.Sprintf("HTTP server stopped: %v", err))
		}
	}()

	a.ready = true
	logger.SysInfo("app", "Controlplane is ready to receive traffic")
	return nil
}

// Stop shutdown toàn bộ runtime theo thứ tự an toàn.
//
// Thứ tự hiện tại:
// 1) mark not-ready
// 2) shutdown HTTP server (timeout 20s)
// 3) stop gRPC server
// 4) stop modules
// 5) shutdown OTel (timeout 10s) + clear Prometheus state
// 6) cancel root context
// 7) close Postgres + Redis
//
// Hàm này nil-safe để dùng được cả trong startup error path và shutdown bình thường.
func (a *App) Stop() {
	if a == nil {
		return
	}
	a.ready = false

	if a.httpServer != nil {
		httpCtx, httpCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer httpCancel()
		if err := a.httpServer.Shutdown(httpCtx); err != nil {
			logger.SysError("app", fmt.Sprintf("HTTP server shutdown error: %v", err))
		}
	}

	if a.grpc != nil {
		a.grpc.Stop()
	}

	if a.modules != nil {
		a.modules.Stop()
	}

	if a.otel != nil {
		otelCtx, otelCancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := a.otel.Shutdown(otelCtx); err != nil {
			logger.SysError("app", fmt.Sprintf("OTel shutdown error: %v", err))
		}
		otelCancel()
	}
	observability.ClearCurrentPrometheus()

	if a.cancel != nil {
		a.cancel()
	}

	if a.psql != nil {
		a.psql.Close()
	}
	if a.rds != nil {
		_ = a.rds.Close()
	}
}
