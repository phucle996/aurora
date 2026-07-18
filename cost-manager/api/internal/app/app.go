package app

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"cost-manager/api/infra"
	"cost-manager/api/internal/config"
	"cost-manager/api/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	googlegrpc "google.golang.org/grpc"
)

type App struct {
	Cfg         *config.Config
	dbPool      *pgxpool.Pool
	redisClient *redis.Client
	natsConn    *nats.Conn
	module      *Module

	httpServer   *http.Server
	grpcServer   *googlegrpc.Server
	rustCmd      *exec.Cmd
	outboxCancel context.CancelFunc
	outboxDone   chan struct{}
}

func NewApp() *App {
	return &App{}
}

func (a *App) Init() error {
	const op = "app.init"

	// 1. Load config
	a.Cfg = config.LoadConfig()

	// 2. Connect to Billing Database Infrastructure
	dbPool, err := infra.ConnectPostgres(&a.Cfg.Psql)

	if err != nil {
		return err
	}
	a.dbPool = dbPool

	// 3. Chạy embedded SQL migrations cho billing schema (idempotent, advisory lock safe)
	if err := runBillingMigrations(context.Background(), dbPool); err != nil {
		return err
	}

	// 4. Connect to Redis Cache Infrastructure
	redisClient, err := infra.ConnectRedis(a.Cfg.Redis.Addr)
	if err != nil {
		return err
	}
	a.redisClient = redisClient

	// 5. Connect to NATS Messaging Infrastructure
	natsConn, err := infra.ConnectNats(a.Cfg.NATS.Addr)
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

	logger.SysInfo(op, "Cost Manager API initialized successfully without Controlplane DB connection (Fully Decoupled).")
	return nil
}

func (a *App) Start() {
	const op = "app.start"

	// 1. Outbox relay cho Pricing updates
	outboxCtx, outboxCancel := context.WithCancel(context.Background())
	a.outboxCancel = outboxCancel
	a.outboxDone = make(chan struct{})
	if a.module != nil && a.module.PricingOutboxRelay != nil {
		go func() {
			defer close(a.outboxDone)
			a.module.PricingOutboxRelay.Run(outboxCtx)
		}()
	} else {
		close(a.outboxDone)
	}

	// 2. Start Billing JetStream consumers.
	if a.module != nil && a.module.PersonalWalletProvisionSubscriber != nil {
		if err := a.module.PersonalWalletProvisionSubscriber.Start(); err != nil {
			logger.SysWarn(op, "Start PersonalWalletProvisionSubscriber failed: "+err.Error())
		}
	}

	if a.module != nil && a.module.ResourceOwnershipSubscriber != nil {
		if err := a.module.ResourceOwnershipSubscriber.Start(context.Background()); err != nil {
			logger.SysWarn(op, "Start ResourceOwnershipSubscriber failed: "+err.Error())
		}
	}

	// 3. Start gRPC Reconciler Worker
	if a.module != nil && a.module.ReconcilerWorker != nil {
		a.module.ReconcilerWorker.Start(context.Background())
	}

	// 2. Start REST HTTP Server
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())

	RegisterRoutes(router, a.module)

	portStr := strconv.Itoa(a.Cfg.App.HTTPPort)
	a.httpServer = &http.Server{
		Addr:    ":" + portStr,
		Handler: router,
	}

	go func() {
		logger.SysInfo(op, "HTTP REST Server is listening on port :"+portStr)
		if err := a.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.SysFatal(op, "HTTP REST Server failure: "+err.Error())
		}
	}()

	// 3. Start Rust Engine child process
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

	if a.module != nil && a.module.ResourceOwnershipSubscriber != nil {
		a.module.ResourceOwnershipSubscriber.Stop()
	}
	if a.module != nil && a.module.PersonalWalletProvisionSubscriber != nil {
		a.module.PersonalWalletProvisionSubscriber.Stop()
	}

	if a.module != nil && a.module.ReconcilerWorker != nil {
		a.module.ReconcilerWorker.Stop()
	}

	// Terminate Rust Engine child process
	if a.rustCmd != nil && a.rustCmd.Process != nil {
		logger.SysInfo(op, "Terminating Rust Engine child process...")
		_ = a.rustCmd.Process.Signal(syscall.SIGTERM)
		_ = a.rustCmd.Wait()
		logger.SysInfo(op, "Rust Engine terminated.")
	}

	// Shutdown HTTP Server
	if a.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.httpServer.Shutdown(ctx); err != nil {
			logger.SysError(op, "HTTP Server shutdown error: "+err.Error())
		}
	}

	if a.grpcServer != nil {
		a.grpcServer.GracefulStop()
	}

	if a.dbPool != nil {
		a.dbPool.Close()
	}

	if a.redisClient != nil {
		_ = a.redisClient.Close()
	}

	if a.natsConn != nil {
		a.natsConn.Close()
	}

	logger.SysInfo(op, "Cost Manager API fully terminated.")
}
