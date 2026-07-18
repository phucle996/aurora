package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"cost-manager/api/infra"
	"cost-manager/api/internal/config"
	"cost-manager/api/internal/service"
	"cost-manager/api/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	googlegrpc "google.golang.org/grpc"
)

type App struct {
	Cfg                *config.Config
	dbPool             *pgxpool.Pool
	controlplaneDBPool *pgxpool.Pool
	redisClient        *redis.Client
	natsConn           *nats.Conn
	module             *Module

	httpServer      *http.Server
	grpcServer      *googlegrpc.Server
	rustCmd         *exec.Cmd
	outboxCancel    context.CancelFunc
	outboxDone      chan struct{}
	ownershipCancel context.CancelFunc
	ownershipDone   chan struct{}
}

func NewApp() *App {
	return &App{}
}

func (a *App) Init() error {
	const op = "app.init"

	// 1. Load config
	a.Cfg = config.LoadConfig()

	// 2. Connect to Database Infrastructure
	dbPool, err := infra.ConnectPostgres(a.Cfg.DBURL)
	if err != nil {
		return err
	}
	a.dbPool = dbPool

	// Controlplane connection is used only by the ownership reconciler, never inside per-usage billing transactions.
	controlplaneDBPool, err := infra.ConnectPostgres(a.Cfg.ControlplaneDBURL)
	if err != nil {
		return fmt.Errorf("app.init: connect controlplane ownership source: %w", err)
	}
	a.controlplaneDBPool = controlplaneDBPool

	// 3. Chạy embedded SQL migrations cho billing schema (idempotent, advisory lock safe)
	if err := runBillingMigrations(context.Background(), dbPool); err != nil {
		return fmt.Errorf("app.init: billing migration failed: %w", err)
	}

	// 4. Connect to Redis Cache Infrastructure
	redisClient, err := infra.ConnectRedis(a.Cfg.RedisURL)
	if err != nil {
		return err
	}
	a.redisClient = redisClient

	// 5. Connect to NATS Messaging Infrastructure
	natsConn, err := infra.ConnectNats(a.Cfg.NatsURL)
	if err != nil {
		return err
	}
	a.natsConn = natsConn

	// 6. Initialize Modules
	module, err := NewModule(a.dbPool, a.natsConn, a.redisClient)
	if err != nil {
		return err
	}
	a.module = module

	return nil
}

func (a *App) Start() {
	const op = "app.start"

	// Outbox relay chạy trên mọi API replica; PostgreSQL SKIP LOCKED phân phối batch an toàn theo HA.
	outboxCtx, outboxCancel := context.WithCancel(context.Background())
	a.outboxCancel = outboxCancel
	a.outboxDone = make(chan struct{})
	go func() {
		defer close(a.outboxDone)
		service.NewPricingOutboxRelay(a.dbPool, a.natsConn).Run(outboxCtx)
	}()

	ownershipCtx, ownershipCancel := context.WithCancel(context.Background())
	a.ownershipCancel = ownershipCancel
	a.ownershipDone = make(chan struct{})
	go func() {
		defer close(a.ownershipDone)
		service.NewOwnershipProjector(a.controlplaneDBPool, a.dbPool).Run(ownershipCtx)
	}()

	// 1. Start HTTP REST Server
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())

	RegisterRoutes(router, a.module)

	a.httpServer = &http.Server{
		Addr:    ":" + a.Cfg.Port,
		Handler: router,
	}

	go func() {
		logger.SysInfo(op, "HTTP REST Server is listening on port :"+a.Cfg.Port)
		if err := a.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.SysFatal(op, "HTTP REST Server failure: "+err.Error())
		}
	}()

	// 4. Start Rust Engine as child process
	rustPath := "cost-manager-engine"
	if _, err := exec.LookPath(rustPath); err != nil {
		rustPath = "../engine/target/release/cost-manager-engine"
		if _, err := os.Stat(rustPath); err != nil {
			rustPath = "../engine/target/debug/cost-manager-engine"
		}
	}

	a.rustCmd = exec.Command(rustPath)
	a.rustCmd.Stdout = os.Stdout
	a.rustCmd.Stderr = os.Stderr
	a.rustCmd.Env = os.Environ()

	if err := a.rustCmd.Start(); err != nil {
		logger.SysWarn(op, "Could not start Rust Engine child process: "+err.Error())
	} else {
		logger.SysInfo(op, "Rust Engine child process successfully started")
	}
}

func (a *App) Wait() {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
}

func (a *App) Stop() {
	const op = "app.stop"
	logger.SysInfo(op, "Shutting down Cost Manager API gracefully...")

	// 1. Dừng outbox relay trước khi đóng NATS/PostgreSQL để không publish trên connection đang teardown.
	if a.outboxCancel != nil {
		a.outboxCancel()
	}
	if a.outboxDone != nil {
		select {
		case <-a.outboxDone:
		case <-time.After(3 * time.Second):
			logger.SysWarn(op, "Timed out waiting for pricing outbox relay")
		}
	}
	if a.ownershipCancel != nil {
		a.ownershipCancel()
	}
	if a.ownershipDone != nil {
		select {
		case <-a.ownershipDone:
		case <-time.After(3 * time.Second):
			logger.SysWarn(op, "Timed out waiting for ownership projector")
		}
	}

	// 2. Terminate Rust Engine child process
	if a.rustCmd != nil && a.rustCmd.Process != nil {
		logger.SysInfo(op, "Terminating Rust Engine child process...")
		_ = a.rustCmd.Process.Signal(syscall.SIGTERM)
		_ = a.rustCmd.Wait()
		logger.SysInfo(op, "Rust Engine terminated.")
	}

	// 3. Shutdown HTTP Server
	if a.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.httpServer.Shutdown(ctx); err != nil {
			logger.SysError(op, "HTTP Server shutdown error: "+err.Error())
		}
	}

	// 4. Stop gRPC Server
	if a.grpcServer != nil {
		a.grpcServer.GracefulStop()
	}

	// 5. Close database connections
	if a.dbPool != nil {
		a.dbPool.Close()
	}
	if a.controlplaneDBPool != nil {
		a.controlplaneDBPool.Close()
	}

	// 6. Close Redis connection
	if a.redisClient != nil {
		_ = a.redisClient.Close()
	}

	// 7. Close NATS connection
	if a.natsConn != nil {
		a.natsConn.Close()
	}

	logger.SysInfo(op, "Cost Manager API fully terminated.")
}
