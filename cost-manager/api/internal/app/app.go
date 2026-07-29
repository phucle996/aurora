package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"cost-manager/api/infra"
	"cost-manager/api/internal/config"
	"cost-manager/api/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	googlegrpc "google.golang.org/grpc"
)

type App struct {
	Cfg             *config.Config
	dbPool          *pgxpool.Pool
	redisClient     *redis.Client
	authRedisClient *redis.Client
	module          *Module

	httpServer         *http.Server
	grpcServer         *googlegrpc.Server
	rustCmd            *exec.Cmd
	outboxCancel       context.CancelFunc
	outboxDone         chan struct{}
	pricingCacheCancel context.CancelFunc
	pricingCacheDone   chan struct{}
}

func NewApp() *App {
	return &App{}
}

func (a *App) Init() error {
	const op = "app.init"

	// 1. Load config
	a.Cfg = config.LoadConfig()
	vaultClient, err := infra.NewVaultClient(context.Background(), a.Cfg.Vault)
	if err != nil {
		return fmt.Errorf("initialize Vault client: %w", err)
	}
	if err := infra.ReadPaymentSecrets(context.Background(), vaultClient, &a.Cfg.Payment); err != nil {
		return fmt.Errorf("initialize payment signing secrets: %w", err)
	}

	// 2. Connect to Billing Database Infrastructure
	dbPool, err := infra.ConnectPostgres(context.Background(), vaultClient, &a.Cfg.Psql)

	if err != nil {
		return err
	}
	a.dbPool = dbPool

	// 3. Chạy embedded SQL migrations cho billing schema (idempotent, advisory lock safe)
	if err := runBillingMigrations(context.Background(), dbPool); err != nil {
		return err
	}

	// 4. Connect to Redis Cache Infrastructure
	redisClient, err := infra.ConnectRedis(context.Background(), vaultClient, infra.SharedL2ConnectionPath)
	if err != nil {
		return err
	}
	a.redisClient = redisClient

	// [COMMENT]: Auth Redis dùng ACL riêng; Cost không có quyền truy cập session namespace.
	authRedisClient, err := infra.ConnectRedis(context.Background(), vaultClient, infra.AuthStateConnectionPath)
	if err != nil {
		return err
	}
	a.authRedisClient = authRedisClient

	// 5. Initialize Modules. Ownership is Central-internal and uses Shared Redis;
	// Cost Manager no longer receives a cross-boundary NATS credential.
	module, err := NewModule(a.dbPool, a.redisClient, a.authRedisClient, a.Cfg.Payment)
	if err != nil {
		return err
	}
	a.module = module

	logger.SysInfo(op, "Cost Manager API initialized successfully without Controlplane DB connection (Fully Decoupled).")
	return nil
}

func (a *App) Start() error {
	const op = "app.start"

	// [COMMENT]: Shared Redis consumer group phải sẵn sàng trước HTTP readiness;
	// pending command sẽ được XAUTOCLAIM khi pod cũ chết hoặc rolling restart.
	if a.module != nil && a.module.PersonalWalletProvisionConsumer != nil {
		if err := a.module.PersonalWalletProvisionConsumer.Start(); err != nil {
			return fmt.Errorf("start personal wallet provision consumer: %w", err)
		}
	}
	if a.module != nil && a.module.TenantWalletProvisionConsumer != nil {
		if err := a.module.TenantWalletProvisionConsumer.Start(); err != nil {
			if a.module.PersonalWalletProvisionConsumer != nil {
				a.module.PersonalWalletProvisionConsumer.Stop()
			}
			return fmt.Errorf("start tenant wallet provision consumer: %w", err)
		}
	}
	if a.module != nil && a.module.ResourceOwnershipConsumer != nil {
		if err := a.module.ResourceOwnershipConsumer.Start(); err != nil {
			if a.module.TenantWalletProvisionConsumer != nil {
				a.module.TenantWalletProvisionConsumer.Stop()
			}
			if a.module.PersonalWalletProvisionConsumer != nil {
				a.module.PersonalWalletProvisionConsumer.Stop()
			}
			return fmt.Errorf("start resource ownership consumer: %w", err)
		}
	}

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

	// Pricing cache invalidation is a best-effort Pub/Sub hint; TTL/cold-start remains the recovery path.
	pricingCacheCtx, pricingCacheCancel := context.WithCancel(context.Background())
	a.pricingCacheCancel = pricingCacheCancel
	a.pricingCacheDone = make(chan struct{})
	if a.module != nil && a.module.TierService != nil {
		go func() {
			defer close(a.pricingCacheDone)
			a.module.TierService.RunPricingCacheInvalidation(pricingCacheCtx)
		}()
	} else {
		close(a.pricingCacheDone)
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
	// The embedded engine is a separate Vault consumer and must not inherit the
	// API token. Its policy is supplied through a dedicated dev deployment
	// variable; production should use a separate workload identity.
	filteredEnv := a.rustCmd.Env[:0]
	engineToken := strings.TrimSpace(os.Getenv("VAULT_ENGINE_TOKEN"))
	for _, item := range a.rustCmd.Env {
		if strings.HasPrefix(item, "VAULT_ENGINE_TOKEN=") ||
			(engineToken != "" && strings.HasPrefix(item, "VAULT_TOKEN=")) {
			continue
		}
		filteredEnv = append(filteredEnv, item)
	}
	if engineToken != "" {
		a.rustCmd.Env = append(filteredEnv, "VAULT_TOKEN="+engineToken)
	}

	if err := a.rustCmd.Start(); err != nil {
		logger.SysWarn(op, "Could not start Rust Engine child process: "+err.Error())
	} else {
		logger.SysInfo(op, "Rust Engine child process successfully started")
	}
	return nil
}

func (a *App) Wait() {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
}

func (a *App) Stop() {
	const op = "app.stop"
	logger.SysInfo(op, "Shutting down Cost Manager API gracefully...")

	if a.pricingCacheCancel != nil {
		a.pricingCacheCancel()
	}
	if a.pricingCacheDone != nil {
		select {
		case <-a.pricingCacheDone:
		case <-time.After(2 * time.Second):
			logger.SysWarn(op, "Timed out waiting for pricing cache invalidation worker")
		}
	}

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

	if a.module != nil && a.module.ResourceOwnershipConsumer != nil {
		a.module.ResourceOwnershipConsumer.Stop()
	}
	if a.module != nil && a.module.TenantWalletProvisionConsumer != nil {
		a.module.TenantWalletProvisionConsumer.Stop()
	}
	if a.module != nil && a.module.PersonalWalletProvisionConsumer != nil {
		a.module.PersonalWalletProvisionConsumer.Stop()
	}

	if a.module != nil && a.module.ReconcilerWorker != nil {
		a.module.ReconcilerWorker.Stop()
	}
	if a.module != nil && a.module.AuthorizationResolver != nil {
		a.module.AuthorizationResolver.Close()
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
	if a.authRedisClient != nil {
		_ = a.authRedisClient.Close()
	}

	logger.SysInfo(op, "Cost Manager API fully terminated.")
}
