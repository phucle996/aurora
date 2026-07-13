package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"cost-manager/api/grpc"
	"cost-manager/api/handler"
	"cost-manager/api/proto/billingproto"
	"cost-manager/api/repository"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	googlegrpc "google.golang.org/grpc"
)

func main() {
	log.Println("Bắt đầu khởi chạy Cost Manager API...")

	// [COMMENT]: 1. Lấy thông tin chuỗi kết nối từ Environment
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://billing_admin:billing_secure_password@billing-psql:5432/billing?sslmode=disable"
	}

	// [COMMENT]: 2. Khởi tạo kết nối pgxpool tới billing-psql
	config, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		log.Fatalf("Không thể parse DATABASE_URL: %v", err)
	}
	config.MaxConns = 20
	config.MinConns = 2
	config.MaxConnIdleTime = 15 * time.Minute

	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		log.Fatalf("Kết nối tới billing-psql thất bại: %v", err)
	}
	defer pool.Close()

	// [COMMENT]: Ping kiểm tra kết nối DB
	if err := pool.Ping(context.Background()); err != nil {
		log.Fatalf("Ping database billing-psql thất bại: %v", err)
	}
	log.Println("Kết nối tới database billing-psql thành công!")

	// [COMMENT]: 3. Khởi tạo repository & handler
	repo := repository.NewWalletRepository(pool)
	walletHandler := handler.NewWalletHandler(repo)

	// [COMMENT]: 4. Cấu hình HTTP Gin Router
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())

	// [COMMENT]: Đăng ký middleware CORS cơ bản để frontend local gọi được
	router.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	// [COMMENT]: Đăng ký REST API endpoints
	api := router.Group("/api/v1/billing")
	{
		api.GET("/wallet", walletHandler.GetWallet)
		api.POST("/wallet/deposit", walletHandler.Deposit)
		api.GET("/wallet/:id/transactions", walletHandler.GetTransactions)
	}

	// [COMMENT]: 5. Khởi chạy HTTP Server trên cổng 8084 bất đồng bộ
	httpServer := &http.Server{
		Addr:    ":8084",
		Handler: router,
	}
	go func() {
		log.Println("HTTP Server đang lắng nghe trên cổng :8084")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP Server chạy lỗi: %v", err)
		}
	}()

	// [COMMENT]: 6. Khởi chạy gRPC Server trên cổng 9094 bất đồng bộ
	grpcListener, err := net.Listen("tcp", ":9094")
	if err != nil {
		log.Fatalf("Không thể khởi động tcp listener cho gRPC: %v", err)
	}
	grpcServer := googlegrpc.NewServer()
	billingGrpcServer := grpc.NewBillingGrpcServer(repo)
	billingproto.RegisterBillingServiceServer(grpcServer, billingGrpcServer)

	go func() {
		log.Println("gRPC Server đang lắng nghe trên cổng :9094")
		if err := grpcServer.Serve(grpcListener); err != nil {
			log.Fatalf("gRPC Server chạy lỗi: %v", err)
		}
	}()

	// [COMMENT]: 7. Khởi chạy Rust Engine (cost-manager-engine) dưới dạng child process
	// Thử tìm trong PATH trước (cho production/docker), nếu không thấy tìm ở relative debug path (cho local dev)
	rustPath := "cost-manager-engine"
	if _, err := exec.LookPath(rustPath); err != nil {
		rustPath = "../engine/target/debug/cost-manager-engine"
	}

	rustCmd := exec.Command(rustPath)
	rustCmd.Stdout = os.Stdout
	rustCmd.Stderr = os.Stderr
	rustCmd.Env = os.Environ()

	if err := rustCmd.Start(); err != nil {
		log.Printf("CẢNH BÁO: Không thể khởi chạy Rust Engine (child process): %v. Có thể file binary chưa được build hoặc không nằm trong PATH.", err)
	} else {
		log.Printf("Rust Engine (child process) đã được kích hoạt thành công (PID: %d)", rustCmd.Process.Pid)
	}

	// [COMMENT]: 8. Xử lý Shutdown an toàn (Graceful Shutdown)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Bắt đầu tắt dịch vụ Cost Manager API an toàn...")

	// [COMMENT]: Gửi tín hiệu tắt cho Rust Engine và đợi nó dừng hẳn
	if rustCmd.Process != nil {
		log.Printf("Đang tắt Rust Engine (PID: %d)...", rustCmd.Process.Pid)
		_ = rustCmd.Process.Signal(syscall.SIGTERM)
		_ = rustCmd.Wait()
		log.Println("Rust Engine đã dừng.")
	}

	// Tắt HTTP Server giới hạn 5 giây
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("HTTP Server Shutdown lỗi: %v", err)
	}

	// Tắt gRPC Server
	grpcServer.GracefulStop()

	log.Println("Cost Manager API đã dừng hoạt động hoàn toàn.")
}
