package app

import (
	"fmt"

	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingSvcInterface "cost-manager/api/internal/domain/service"
	"cost-manager/api/internal/repository"
	"cost-manager/api/internal/service"
	"cost-manager/api/internal/transport/http/handler"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
)

// Module quản lý tất cả các repository, service và handler của ứng dụng
type Module struct {
	PlanRepo    billingRepoInterface.PlanRepository
	PlanService billingSvcInterface.PlanService
	PlanHandler *handler.PlanHandler
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

	// Khởi tạo service chứa business logic và tích hợp caching
	planService := service.NewPlanService(planRepo, redisClient)
	if planService == nil {
		return nil, fmt.Errorf("failed to initialize PlanService: instance is nil")
	}

	// Khởi tạo handler xử lý HTTP request/response
	planHandler := handler.NewPlanHandler(planService)
	if planHandler == nil {
		return nil, fmt.Errorf("failed to initialize PlanHandler: instance is nil")
	}

	return &Module{
		PlanRepo:    planRepo,
		PlanService: planService,
		PlanHandler: planHandler,
	}, nil
}
