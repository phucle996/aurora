package app

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"cost-manager/api/grpc"
	"cost-manager/api/infra"
	"cost-manager/api/internal/config"
	"cost-manager/api/pkg/logger"
	"cost-manager/api/internal/transport/proto/billingproto"

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

	httpServer *http.Server
	grpcServer *googlegrpc.Server
	rustCmd    *exec.Cmd
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

	// 3. Connect to Redis Cache Infrastructure
	redisClient, err := infra.ConnectRedis(a.Cfg.RedisURL)
	if err != nil {
		logger.SysWarn(op, "Failed to connect to Redis: "+err.Error())
	} else {
		a.redisClient = redisClient
	}

	// 4. Connect to NATS Messaging Infrastructure
	natsConn, err := infra.ConnectNats(a.Cfg.NatsURL)
	if err != nil {
		logger.SysWarn(op, "Failed to connect to NATS: "+err.Error())
	} else {
		a.natsConn = natsConn
	}

	// 5. Initialize Modules
	a.module = NewModule(a.dbPool, a.natsConn, a.redisClient)

	return nil
}

func (a *App) Start() {
	const op = "app.start"

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

	// 2. Start gRPC Server
	grpcListener, err := net.Listen("tcp", ":9094")
	if err != nil {
		logger.SysFatal(op, "Failed to listen TCP port for gRPC: "+err.Error())
	}
	a.grpcServer = googlegrpc.NewServer()
	billingGrpcServer := grpc.NewBillingGrpcServer(a.module.WalletRepo)
	billingproto.RegisterBillingServiceServer(a.grpcServer, billingGrpcServer)

	go func() {
		logger.SysInfo(op, "gRPC Server is listening on port :9094")
		if err := a.grpcServer.Serve(grpcListener); err != nil {
			logger.SysFatal(op, "gRPC Server failure: "+err.Error())
		}
	}()

	// 3. Start Rust Engine as child process
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

	// 1. Terminate Rust Engine child process
	if a.rustCmd != nil && a.rustCmd.Process != nil {
		logger.SysInfo(op, "Terminating Rust Engine child process...")
		_ = a.rustCmd.Process.Signal(syscall.SIGTERM)
		_ = a.rustCmd.Wait()
		logger.SysInfo(op, "Rust Engine terminated.")
	}

	// 2. Shutdown HTTP Server
	if a.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.httpServer.Shutdown(ctx); err != nil {
			logger.SysError(op, "HTTP Server shutdown error: "+err.Error())
		}
	}

	// 3. Stop gRPC Server
	if a.grpcServer != nil {
		a.grpcServer.GracefulStop()
	}

	// 4. Close database connections
	if a.dbPool != nil {
		a.dbPool.Close()
	}

	// 5. Close Redis connection
	if a.redisClient != nil {
		_ = a.redisClient.Close()
	}

	// 6. Close NATS connection
	if a.natsConn != nil {
		a.natsConn.Close()
	}

	logger.SysInfo(op, "Cost Manager API fully terminated.")
}
