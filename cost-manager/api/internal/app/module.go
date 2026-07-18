package app

import (
	"fmt"

	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingSvcInterface "cost-manager/api/internal/domain/service"
	"cost-manager/api/internal/repository"
	"cost-manager/api/internal/service"
	"cost-manager/api/internal/transport/http/handler"
	natsHandler "cost-manager/api/internal/transport/nats/handler"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
)

// Module quản lý tất cả các repository, service và handler của ứng dụng
type Module struct {
	PlanRepo    billingRepoInterface.PlanRepository
	PlanService billingSvcInterface.PlanService
	PlanHandler *handler.PlanHandler

	// [COMMENT]: Đăng ký thêm các trường cho thực thể cước lũy tiến (Tier)
	TierRepo    billingRepoInterface.TierRepository
	TierService billingSvcInterface.TierService
	TierHandler *handler.TierHandler
}

// NewModule khởi tạo Module và thực hiện Dependency Injection
func NewModule(dbPool *pgxpool.Pool, natsConn *nats.Conn, redisClient *redis.Client) (*Module, error) {
	if dbPool == nil {
		return nil, fmt.Errorf("dbPool infrastructure connection cannot be nil")
	}
	if redisClient == nil {
		return nil, fmt.Errorf("redisClient infrastructure connection cannot be nil")
	}
	if natsConn == nil {
		return nil, fmt.Errorf("natsConn infrastructure connection cannot be nil")
	}

	// Khởi tạo repository kết nối DB
	planRepo := repository.NewPlanRepository(dbPool)
	if planRepo == nil {
		return nil, fmt.Errorf("failed to initialize PlanRepository: instance is nil")
	}

	// [COMMENT]: Khởi tạo TierRepository kết nối PostgreSQL
	tierRepo := repository.NewTierRepository(dbPool)
	if tierRepo == nil {
		return nil, fmt.Errorf("failed to initialize TierRepository: instance is nil")
	}

	// Khởi tạo service chứa business logic và tích hợp caching
	planService := service.NewPlanService(planRepo, redisClient)
	if planService == nil {
		return nil, fmt.Errorf("failed to initialize PlanService: instance is nil")
	}

	// [COMMENT]: Khởi tạo TierService chứa nghiệp vụ Tiers
	tierService := service.NewTierService(tierRepo)
	if tierService == nil {
		return nil, fmt.Errorf("failed to initialize TierService: instance is nil")
	}

	// Khởi tạo handler xử lý HTTP request/response
	planHandler := handler.NewPlanHandler(planService)
	if planHandler == nil {
		return nil, fmt.Errorf("failed to initialize PlanHandler: instance is nil")
	}

	// [COMMENT]: Khởi tạo TierHandler xử lý request cước lũy tiến
	tierHandler := handler.NewTierHandler(tierService)
	if tierHandler == nil {
		return nil, fmt.Errorf("failed to initialize TierHandler: instance is nil")
	}

	// Khởi tạo Auth Repository
	authRepo := repository.NewAuthRepository(dbPool)
	if authRepo == nil {
		return nil, fmt.Errorf("failed to initialize AuthRepository: instance is nil")
	}

	// Khởi tạo Auth Service
	authService := service.NewAuthService(authRepo)
	if authService == nil {
		return nil, fmt.Errorf("failed to initialize AuthService: instance is nil")
	}

	// Đăng ký NATS subscriber phục vụ xác thực người dùng cho cost console sử dụng Protobuf
	_, err := natsHandler.SubscribeAuth(natsConn, authService)
	if err != nil {
		return nil, fmt.Errorf("failed to register NATS auth subscriber: %w", err)
	}

	return &Module{
		PlanRepo:    planRepo,
		PlanService: planService,
		PlanHandler: planHandler,

		// [COMMENT]: Gán các dependencies đã khởi tạo của Tier vào Module
		TierRepo:    tierRepo,
		TierService: tierService,
		TierHandler: tierHandler,
	}, nil
}
