package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"cost-manager/api/infra"
	"cost-manager/api/internal/config"
	"cost-manager/api/internal/transport/http/handler"
	"cost-manager/api/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	googlegrpc "google.golang.org/grpc"
)

type App struct {
	Cfg               *config.Config
	dbPool            *pgxpool.Pool
	redisClient       *redis.Client
	authRedisClient   *redis.Client
	resourcePlanRedis redis.UniversalClient
	module            *Module

	httpServer            *http.Server
	grpcServer            *googlegrpc.Server
	costEngine            *costEngineProcess
	outboxCancel          context.CancelFunc
	outboxDone            chan struct{}
	walletAdmissionCancel context.CancelFunc
	walletAdmissionDone   chan struct{}
	pricingCacheCancel    context.CancelFunc
	pricingCacheDone      chan struct{}
}

func NewApp() *App {
	return &App{}
}

func (a *App) Init() error {
	const op = "app.init"

	// 1. Load config
	a.Cfg = config.LoadConfig()
	// The embedded Engine is a separate Vault consumer. Validate its
	// app-scoped identity before opening any database/Redis connection so a
	// missing child credential cannot leave a partially initialized API alive.
	costEngine, err := newCostEngineProcess(os.Environ())
	if err != nil {
		return err
	}
	a.costEngine = costEngine
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
	// Dedicated resource-plan connection; other workflows keep their own clients.
	planOptions := redisClient.Options()
	if a.Cfg.ResourcePlanRelay.Cluster {
		a.resourcePlanRedis = redis.NewClusterClient(&redis.ClusterOptions{Addrs: []string{planOptions.Addr}, Username: planOptions.Username, Password: planOptions.Password, TLSConfig: planOptions.TLSConfig, ContextTimeoutEnabled: true})
	} else {
		a.resourcePlanRedis = redisClient
	}

	// [COMMENT]: Auth Redis dùng ACL riêng; Cost không có quyền truy cập session namespace.
	authRedisClient, err := infra.ConnectRedis(context.Background(), vaultClient, infra.AuthStateConnectionPath)
	if err != nil {
		return err
	}
	a.authRedisClient = authRedisClient

	// 5. Initialize Modules. Ownership is Central-internal and uses Shared Redis;
	// Cost Manager no longer receives a cross-boundary NATS credential.
	module, err := NewModule(a.dbPool, a.redisClient, a.authRedisClient, a.Cfg.Payment, a.resourcePlanRedis, a.Cfg.ResourcePlanRelay)
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

	// Each Cost workflow owns its own durable relay.
	outboxCtx, outboxCancel := context.WithCancel(context.Background())
	a.outboxCancel = outboxCancel
	a.outboxDone = make(chan struct{})
	go func() {
		defer close(a.outboxDone)
		var workers sync.WaitGroup
		if a.module != nil && a.module.StoragePricingService != nil {
			workers.Add(1)
			go func() { defer workers.Done(); a.module.StoragePricingService.RunPricingOutboxRelay(outboxCtx) }()
		}
		if a.module != nil && a.module.HypervisorPricingService != nil {
			workers.Add(1)
			go func() { defer workers.Done(); a.module.HypervisorPricingService.RunPricingOutboxRelay(outboxCtx) }()
		}
		if a.module != nil && a.module.HypervisorResourcePlanService != nil {
			workers.Add(1)
			go func() {
				defer workers.Done()
				a.module.HypervisorResourcePlanService.RunHypervisorResourcePlanOutboxRelay(outboxCtx)
			}()
		}
		if a.module != nil && a.module.MailPricingService != nil {
			workers.Add(1)
			go func() { defer workers.Done(); a.module.MailPricingService.RunPricingOutboxRelay(outboxCtx) }()
		}
		workers.Wait()
	}()

	// Wallet admission relay publishes only committed, versioned wallet
	// transitions. Billing PostgreSQL remains the replay authority.
	walletAdmissionCtx, walletAdmissionCancel := context.WithCancel(context.Background())
	a.walletAdmissionCancel = walletAdmissionCancel
	a.walletAdmissionDone = make(chan struct{})
	if a.module != nil && a.module.WalletAdmissionOutboxRelay != nil {
		go func() {
			defer close(a.walletAdmissionDone)
			a.module.WalletAdmissionOutboxRelay.Run(walletAdmissionCtx)
		}()
	} else {
		close(a.walletAdmissionDone)
	}

	// Pricing cache invalidation is a best-effort Pub/Sub hint; TTL/cold-start remains the recovery path.
	pricingCacheCtx, pricingCacheCancel := context.WithCancel(context.Background())
	a.pricingCacheCancel = pricingCacheCancel
	a.pricingCacheDone = make(chan struct{})
	go func() {
		defer close(a.pricingCacheDone)
		var workers sync.WaitGroup

		if a.module != nil && a.module.StoragePricingService != nil {
			workers.Add(1)
			go func() {
				defer workers.Done()
				a.module.StoragePricingService.RunPricingCacheInvalidation(pricingCacheCtx)
			}()
		}

		if a.module != nil && a.module.HypervisorPricingService != nil {
			workers.Add(2)
			go func() {
				defer workers.Done()
				a.module.HypervisorPricingService.RunPricingCacheInvalidation(pricingCacheCtx)
			}()
			go func() {
				defer workers.Done()
				a.module.HypervisorPricingService.RunPricingSnapshotRefresh(pricingCacheCtx)
			}()
		}
		if a.module != nil && a.module.MailPricingService != nil {
			workers.Add(2)
			go func() {
				defer workers.Done()
				a.module.MailPricingService.RunPricingCacheInvalidation(pricingCacheCtx)
			}()
			go func() {
				defer workers.Done()
				a.module.MailPricingService.RunPricingSnapshotRefresh(pricingCacheCtx)
			}()
		}
		workers.Wait()
	}()

	if err := a.costEngine.Start(); err != nil {
		return err
	}
	logger.SysInfo(op, "Rust Engine child process successfully started")

	// 2. Start REST HTTP Server only after the critical child process exists.
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	healthHandler := handler.NewHealthHandler(a.dbPool, a.redisClient, a.costEngine)
	RegisterRoutes(router, a.module, healthHandler)

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

	return nil
}

func (a *App) Wait() error {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)
	select {
	case <-quit:
		return nil
	case err := <-a.costEngine.Done():
		if err == nil {
			return errors.New("embedded Cost Engine exited unexpectedly")
		}
		return fmt.Errorf("embedded Cost Engine exited unexpectedly: %w", err)
	}
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

	if a.walletAdmissionCancel != nil {
		a.walletAdmissionCancel()
	}
	if a.walletAdmissionDone != nil {
		select {
		case <-a.walletAdmissionDone:
		case <-time.After(3 * time.Second):
			logger.SysWarn(op, "Timed out waiting for wallet admission relay")
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

	if a.module != nil && a.module.PersonalAuthorizationMiddleware != nil {
		a.module.PersonalAuthorizationMiddleware.Close()
	}
	if a.module != nil && a.module.TenantAuthorizationMiddleware != nil {
		a.module.TenantAuthorizationMiddleware.Close()
	}

	// Terminate the isolated Cost Engine lifecycle workflow.
	a.costEngine.Stop()

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

	if a.resourcePlanRedis != nil && a.resourcePlanRedis != a.redisClient {
		_ = a.resourcePlanRedis.Close()
	}
	if a.redisClient != nil {
		_ = a.redisClient.Close()
	}
	if a.authRedisClient != nil {
		_ = a.authRedisClient.Close()
	}

	logger.SysInfo(op, "Cost Manager API fully terminated.")
}
