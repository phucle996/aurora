// ============================================================================
// 🛡️ ARCHITECTURAL & SYSTEM CONTRACTS
// ============================================================================
//
// 🤝 1. SYSTEM CONTRACT
//   - Root Container: App là Runtime Container duy nhất của tiến trình Controlplane,
//     chịu toàn bộ trách nhiệm về vòng đời hệ thống (Start/Stop).
//   - Startup Ordering: Tuân thủ nghiêm ngặt thứ tự khởi động để tránh race condition
//     hoặc thao tác trên tài nguyên chưa sẵn sàng:
//       Security -> Infra (PSQL / Redis) -> Migrations -> Policy Engine
//       -> Observability -> HTTP Engine -> Modules -> gRPC -> Routes.
//   - Single Cleanup Path: Toàn bộ đường dẫn lỗi bootstrap đều gọi app.Stop() trước khi trả về
//     lỗi để đảm bảo không rò rỉ tài nguyên (Resource Leak).
//
// 🔑 2. FAIL STRATEGY MATRIX
//   [FAIL-CLOSE] Security - RuntimeMasterKey:     Bắt buộc, không có thì hệ thống không start.
//   [FAIL-CLOSE] Infrastructure - PostgreSQL:     Bắt buộc, mất DB thì toàn bộ nghiệp vụ tê liệt.
//   [FAIL-CLOSE] Infrastructure - Redis:          Bắt buộc, mất Redis thì session/cache mất hoàn toàn.
//   [FAIL-CLOSE] Infrastructure - Kafka config:   Client/config bắt buộc; broker outage fail theo publish,
//                                                 không làm bootstrap loop vì pending-login có recovery.
//   [FAIL-CLOSE] Schema Migrations:               Bắt buộc, schema sai thì data corruption ngay lập tức.
//   [FAIL-CLOSE] Policy Engine:                   Bắt buộc, không có policy thì không thể điều phối runtime.
//   [FAIL-OPEN / FAIL-CLOSE] OTel Tracing:        Được điều khiển bởi FailStrategy trong Policy Engine
//                                                 (fail_open -> NullOTel/nil, fail_close -> abort).
//   [FAIL-OPEN / FAIL-CLOSE] Prometheus:          Được điều khiển bởi FailStrategy trong Policy Engine
//                                                 (fail_open -> NullPrometheus, fail_close -> abort).
//   [FAIL-CLOSE] Rate Limiter:                    SetFailOpen(false) -> mất Redis thì chặn toàn bộ request.
//   [FAIL-CLOSE] HTTP Trusted Proxies:            Sai cấu hình IP có thể bypass CIDR guard, phải crash.
//   [FAIL-CLOSE] Module Graph:                    Fail-fast, module lỗi thì toàn bộ cross-module wiring sai.
//
// 📖 3. SOURCE OF TRUTH
//   - Config được nạp từ config.LoadConfig() trước khi gọi NewApplication.
//   - Policy Set được tải từ Policy Engine sau khi Redis và PSQL sẵn sàng.
//
// 🚧 4. SYSTEM BOUNDARY
//   - App không implement nghiệp vụ trực tiếp. Tất cả logic nghiệp vụ đều nằm trong Modules.
//   - App chỉ đảm nhận dây nối (Wiring), thứ tự khởi động và vòng đời tài nguyên.
//
// 💡 5. OPERATIONAL NOTES
//   - Stop() nil-safe và idempotent, có thể gọi từ bất kỳ error path nào trong bootstrap.
//   - Graceful Shutdown: HTTP server có 20s timeout, OTel flush có 10s timeout.

package app

import (
	"context"
	kafkainfra "controlplane/infra/kafka"
	natsinfra "controlplane/infra/nats"
	"controlplane/infra/psql"
	redisinfra "controlplane/infra/redis"
	"controlplane/infra/vault"
	"controlplane/internal/app/bootstrap"
	"controlplane/internal/config"
	"controlplane/internal/http/middleware"
	"controlplane/internal/observability"
	"controlplane/pkg/logger"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	vaultapi "github.com/hashicorp/vault/api"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	goredis "github.com/redis/go-redis/v9"
)

// App là root runtime container của controlplane process.
// Giữ lifecycle context, references đến infra runtime (DB/Redis/OTel/HTTP/gRPC)
// và điều phối start/stop toàn bộ server đúng thứ tự.
type App struct {
	ctx        context.Context
	cancel     context.CancelFunc
	cfg        *config.Config
	modules    *Modules
	otel       *observability.OTel
	prom       *observability.Metrics
	httpServer *http.Server
	grpc       *bootstrap.GRPC
	psql       *pgxpool.Pool
	rds        *goredis.Client
	authRds    *goredis.Client
	kafka      *kafkainfra.Producer
	natsConn   *nats.Conn
	// [COMMENT]: Vault client phục vụ kết nối quản lý khóa an toàn
	vault *vaultapi.Client
	ready bool
}

// NewApplication khởi tạo toàn bộ runtime dependency theo thứ tự đã định và trả về App sẵn sàng Start().
// Nếu bất kỳ bước nào lỗi, hàm gọi app.Stop() để dọn dẹp thống nhất rồi trả lỗi ra ngoài.
func NewApplication(cfg *config.Config) (*App, error) {
	if cfg == nil {
		return nil, fmt.Errorf("bootstrap: config is required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	app := &App{ctx: ctx, cancel: cancel, cfg: cfg}

	// --------------------------------------------------------------------
	// [FAIL-CLOSE] Infrastructure bootstrap: PostgreSQL.
	// Mất kết nối DB -> toàn bộ nghiệp vụ đọc/ghi dữ liệu tê liệt -> abort.
	// --------------------------------------------------------------------
	db, err := psql.NewPostgres(ctx, &cfg.Psql)
	if err != nil {
		app.Stop()
		return nil, fmt.Errorf("bootstrap: psql init failed: %w", err)
	}
	app.psql = db

	// --------------------------------------------------------------------
	// [FAIL-CLOSE] Infrastructure bootstrap: Redis (Session / Cache).
	// Mất Redis chính -> session/token cache mất hoàn toàn -> abort.
	// --------------------------------------------------------------------
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
	// [COMMENT]: Security-State/AuthZ Redis là deployment và credential riêng; không dùng logical DB.
	authRds, err := redisinfra.NewRedis(ctx, &cfg.AuthRedis)
	if err != nil {
		app.Stop()
		return nil, fmt.Errorf("bootstrap: auth redis init failed: %w", err)
	}
	app.authRds = authRds
	if authRds == nil {
		app.Stop()
		return nil, fmt.Errorf("bootstrap: auth redis client is required")
	}

	// [COMMENT]: Chỉ fail-fast cấu hình Kafka/client; broker outage được xử lý tại publish để
	// luồng mail best-effort không kéo sập toàn bộ Controlplane.
	kafkaProducer, err := kafkainfra.NewProducer(ctx, &cfg.Kafka)
	if err != nil {
		app.Stop()
		return nil, fmt.Errorf("bootstrap: kafka init failed: %w", err)
	}
	app.kafka = kafkaProducer
	if kafkaProducer == nil {
		app.Stop()
		return nil, fmt.Errorf("bootstrap: kafka producer is required")
	}

	// [COMMENT]: Khởi tạo NATS Core Client từ infra connector hỗ trợ TLS/mTLS và retry
	nc, err := natsinfra.NewNATS(ctx, &cfg.NATS, cfg.App.AppName)
	if err != nil {
		app.Stop()
		return nil, fmt.Errorf("bootstrap: nats init failed: %w", err)
	}
	app.natsConn = nc

	// --------------------------------------------------------------------
	// [FAIL-CLOSE] Infrastructure bootstrap: HashiCorp Vault.
	// Khởi tạo client kết nối tới Vault phục vụ cho Key Management.
	// --------------------------------------------------------------------
	vaultClient, err := vault.NewVaultClient(ctx, &cfg.Vault)
	if err != nil {
		app.Stop()
		return nil, fmt.Errorf("bootstrap: vault init failed: %w", err)
	}
	app.vault = vaultClient

	// --------------------------------------------------------------------
	// [FAIL-CLOSE] Schema bootstrap: Database migrations bắt buộc chạy trước khi modules dùng DB.
	// Schema sai -> data corruption tức thì -> abort.
	// --------------------------------------------------------------------
	if err := bootstrap.RunMigrations(ctx, db, cfg); err != nil {
		app.Stop()
		return nil, err
	}

	// Khởi tạo OTel trực tiếp bằng struct cấu hình từ config
	otelObs, err := observability.InitOTel(ctx, &cfg.OTel, cfg.App.AppName)
	if err != nil {
		if cfg.OTel.FailStrategy == "fail_open" {
			logger.SysWarn("bootstrap", fmt.Sprintf("otel init failed [FAIL-OPEN]: %v. Tracing disabled, continuing startup.", err))
			otelObs = nil
			err = nil // clear error, tiếp tục startup bình thường
		} else {
			app.Stop()
			return nil, fmt.Errorf("bootstrap: otel init failed [FAIL-CLOSE]: %w", err)
		}
	}
	app.otel = otelObs

	// --------------------------------------------------------------------
	// [FAIL-OPEN / FAIL-CLOSE] Observability bootstrap: OTel Metrics.
	// Chiến lược do cấu hình tĩnh kiểm soát qua cfg.OTel.FailStrategy:
	//   - fail_open  -> lỗi Metrics thì dùng NullMetrics (no-op), hệ thống tiếp tục.
	//   - fail_close -> lỗi Metrics thì abort toàn bộ startup.
	// --------------------------------------------------------------------
	var promObs *observability.Metrics
	promObs, err = observability.InitMetrics(cfg.App.AppName)
	if err != nil {
		if cfg.OTel.FailStrategy == "fail_open" {
			logger.SysWarn("bootstrap", fmt.Sprintf("OTel metrics init failed [FAIL-OPEN]: %v. Falling back to NullMetrics.", err))
			promObs = observability.NullMetrics()
			err = nil // clear error, tiếp tục startup bình thường
		} else {
			app.Stop()
			return nil, fmt.Errorf("bootstrap: OTel metrics init failed [FAIL-CLOSE]: %w", err)
		}
	}
	app.prom = promObs

	// [COMMENT]: Khởi động cấu hình HTTP Engine
	// --------------------------------------------------------------------

	// --------------------------------------------------------------------
	// [FAIL-CLOSE] HTTP Engine bootstrap: TrustedProxies là bắt buộc.
	// Cấu hình sai IP proxy -> CIDR guard bị bypass hoặc IP spoofing -> abort.
	// --------------------------------------------------------------------
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	if engine == nil {
		app.Stop()
		return nil, fmt.Errorf("bootstrap: HTTP engine (Gin) router is nil")
	}
	if err := engine.SetTrustedProxies(cfg.App.TrustedProxies); err != nil {
		app.Stop()
		return nil, fmt.Errorf("bootstrap: set trusted proxies failed: %w", err)
	}

	// Đăng ký global middlewares trước khi register routes.
	// Thứ tự thực thi middleware quan trọng, không được đổi thứ tự tùy tiện.
	engine.Use(
		gin.Recovery(),
		middleware.RequestID(),
		middleware.ContextInjector(),
		middleware.OTelTraceContext(otelObs),
		middleware.OTelHTTPMetrics(promObs),
		middleware.AccessLog(),
		middleware.AdminXSSI(),
	)

	// --------------------------------------------------------------------
	// [FAIL-CLOSE] Phase 1: Khởi tạo Cache Engine
	// Cache Engine lỗi hoặc Redis mất kết nối -> abort lập tức (Fail-Close).
	// --------------------------------------------------------------------
	cacheEngine, err := InitCacheEngine(rds, "cacheengine:l1:fanout")
	if err != nil {
		app.Stop()
		return nil, fmt.Errorf("bootstrap: cache engine init failed [FAIL-CLOSE]: %w", err)
	}

	// --------------------------------------------------------------------
	// [FAIL-CLOSE] Module Graph bootstrap: toàn bộ module khởi tạo và wiring.
	// Lỗi ở đây ảnh hưởng cross-module (IAM, Core security provider, middleware auth) -> abort.
	// --------------------------------------------------------------------

	modules, err := NewGlobalModules(cfg, db, rds, authRds, kafkaProducer, cacheEngine, app.natsConn, app.otel)
	if err != nil {
		app.Stop()
		return nil, err
	}
	app.modules = modules

	// Chạy module-level bootstrap hooks (bao gồm IAM bootstrap contract wiring middleware auth).
	bootstrapCtx, bootstrapCancel := context.WithTimeout(app.ctx, 20*time.Second)
	defer bootstrapCancel()
	if err := RunModuleBootstraps(bootstrapCtx, modules); err != nil {
		app.Stop()
		return nil, err
	}

	// --------------------------------------------------------------------
	// [FAIL-CLOSE] Transport bootstrap: gRPC server.
	// gRPC lỗi -> job-proxy không kết nối được vào controlplane -> abort.
	// --------------------------------------------------------------------
	g, err := bootstrap.InitGRPCServer(&cfg.GRPC, otelObs)
	if err != nil {
		app.Stop()
		return nil, err
	}
	app.grpc = g

	// Register tất cả HTTP routes sau khi modules đã wire xong hoàn toàn.
	NewGlobalRoutes(engine, modules)

	// --------------------------------------------------------------------
	// [FAIL-OPEN] Phase 2: Đăng ký cache loaders và khởi chạy subscription loop
	// --------------------------------------------------------------------
	RegisterL1Loaders(cacheEngine, modules)
	go func() {
		ctx := context.Background()
		if err := cacheEngine.StartSubscribe(ctx); err != nil {
			logger.SysWarn("cacheengine", "subscription loop terminated: "+err.Error())
		}
	}()

	// HTTP server runtime configuration.
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

// Start khởi chạy HTTP và gRPC server bất đồng bộ trên các Goroutine riêng biệt.
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

// Stop shutdown toàn bộ runtime theo thứ tự an toàn (Graceful Shutdown).
//
// Thứ tự hiện tại:
//  1. Mark not-ready (ngừng nhận traffic mới)
//  2. HTTP server shutdown (timeout 20s để drain in-flight requests)
//  3. gRPC server stop
//  4. Modules stop (giải phóng background workers)
//  5. OTel flush + Prometheus state clear
//  6. Cancel root context
//  7. Đóng PSQL pool và Redis connections
//
// Nil-safe và idempotent - có thể gọi từ cả startup error path và shutdown bình thường.
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
	observability.ClearCurrentMetrics()

	if a.cancel != nil {
		a.cancel()
	}

	if a.psql != nil {
		a.psql.Close()
	}
	if a.rds != nil {
		_ = a.rds.Close()
	}
	if a.authRds != nil {
		_ = a.authRds.Close()
	}
	if a.kafka != nil {
		a.kafka.Close()
	}
	if a.natsConn != nil {
		a.natsConn.Close()
	}
}
