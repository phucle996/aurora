/*
============================================================================
MAP: COST MANAGER API CENTRALIZED MODULE & DEPENDENCY INJECTION
============================================================================
CONTRACT:
1. Centralized Dependency Injection container cho toàn bộ ứng dụng Cost Manager API.
2. Khởi tạo và liên kết 3 lớp Repository -> Service -> Handler / Worker cho tất cả phân hệ.
3. Kiểm tra nil đàng hoàng sau mỗi câu lệnh khởi tạo để đảm bảo không instance nào bị nil tại runtime.
============================================================================
*/

package app

import (
	"fmt"

	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingSvcInterface "cost-manager/api/internal/domain/service"
	"cost-manager/api/internal/repository"
	"cost-manager/api/internal/service"
	"cost-manager/api/internal/transport/http/handler"
	redisHandler "cost-manager/api/internal/transport/redis/handler"
	"cost-manager/api/internal/transport/rpc"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// [COMMENT]: Module quản lý tất cả các repository, service và handler của ứng dụng.
type Module struct {
	AccountRepo                     billingRepoInterface.AccountRepository
	AccountService                  billingSvcInterface.AccountService
	AccountHandler                  *handler.AccountHandler
	PersonalWalletProvisionConsumer *redisHandler.PersonalWalletProvisionConsumer

	PlanRepo    billingRepoInterface.PlanRepository
	PlanService billingSvcInterface.PlanService
	PlanHandler *handler.PlanHandler

	TierRepo    billingRepoInterface.TierRepository
	TierService billingSvcInterface.TierService
	TierHandler *handler.TierHandler

	ReconcilerRepo    billingRepoInterface.ReconcilerRepository
	ReconcilerService service.ReconcilerService
	ReconcilerWorker  *rpc.StorageOwnershipReconcilerWorker

	ResourceOwnershipRepo     billingRepoInterface.ResourceOwnershipRepository
	ResourceOwnershipService  service.ResourceOwnershipService
	ResourceOwnershipConsumer *redisHandler.ResourceOwnershipConsumer

	PricingOutboxRepo  billingRepoInterface.PricingOutboxRepository
	PricingOutboxRelay *service.PricingOutboxRelay

	AuthorizationResolver *service.AuthorizationResolver
}

// [COMMENT]: NewModule khởi tạo Module và thực hiện Dependency Injection kèm nil check đầy đủ sau mỗi bước.
func NewModule(
	dbPool *pgxpool.Pool,
	redisClient *redis.Client,
	authRedisClient *redis.Client,
) (*Module, error) {
	if dbPool == nil {
		return nil, fmt.Errorf("dbPool infrastructure connection cannot be nil")
	}
	if redisClient == nil {
		return nil, fmt.Errorf("redisClient infrastructure connection cannot be nil")
	}
	if authRedisClient == nil {
		return nil, fmt.Errorf("authRedisClient infrastructure connection cannot be nil")
	}
	// 1. Account Domain DI
	accountRepo := repository.NewAccountRepository(dbPool)
	if accountRepo == nil {
		return nil, fmt.Errorf("failed to initialize AccountRepository: instance is nil")
	}

	accountService := service.NewAccountService(accountRepo, redisClient)
	if accountService == nil {
		return nil, fmt.Errorf("failed to initialize AccountService: instance is nil")
	}

	accountHandler := handler.NewAccountHandler(accountService)
	if accountHandler == nil {
		return nil, fmt.Errorf("failed to initialize AccountHandler: instance is nil")
	}
	personalWalletProvisionConsumer, err := redisHandler.NewPersonalWalletProvisionConsumer(redisClient, accountService)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize PersonalWalletProvisionConsumer: %w", err)
	}

	// 2. Plan Domain DI
	planRepo := repository.NewPlanRepository(dbPool)
	if planRepo == nil {
		return nil, fmt.Errorf("failed to initialize PlanRepository: instance is nil")
	}

	planService := service.NewPlanService(planRepo, redisClient)
	if planService == nil {
		return nil, fmt.Errorf("failed to initialize PlanService: instance is nil")
	}

	planHandler := handler.NewPlanHandler(planService)
	if planHandler == nil {
		return nil, fmt.Errorf("failed to initialize PlanHandler: instance is nil")
	}

	// 3. Pricing relay được tạo trước Tier service để producer có thể wake relay ngay sau commit.
	pricingOutboxRepo := repository.NewPricingOutboxRepository(dbPool)
	if pricingOutboxRepo == nil {
		return nil, fmt.Errorf("failed to initialize PricingOutboxRepository: instance is nil")
	}

	pricingOutboxRelay := service.NewPricingOutboxRelay(pricingOutboxRepo, redisClient)
	if pricingOutboxRelay == nil {
		return nil, fmt.Errorf("failed to initialize PricingOutboxRelay: instance is nil")
	}

	// 4. Tier Domain DI
	tierRepo := repository.NewTierRepository(dbPool)
	if tierRepo == nil {
		return nil, fmt.Errorf("failed to initialize TierRepository: instance is nil")
	}

	tierService := service.NewTierService(tierRepo, pricingOutboxRelay.Notify)
	if tierService == nil {
		return nil, fmt.Errorf("failed to initialize TierService: instance is nil")
	}

	tierHandler := handler.NewTierHandler(tierService)
	if tierHandler == nil {
		return nil, fmt.Errorf("failed to initialize TierHandler: instance is nil")
	}

	// 5. Reconciler Worker DI (gRPC)
	reconcilerRepo := repository.NewReconcilerRepository(dbPool)
	if reconcilerRepo == nil {
		return nil, fmt.Errorf("failed to initialize ReconcilerRepository: instance is nil")
	}

	reconcilerService := service.NewReconcilerService(reconcilerRepo)
	if reconcilerService == nil {
		return nil, fmt.Errorf("failed to initialize ReconcilerService: instance is nil")
	}

	reconcilerWorker := rpc.NewStorageOwnershipReconcilerWorker(reconcilerService, 0)
	if reconcilerWorker == nil {
		return nil, fmt.Errorf("failed to initialize ReconcilerWorker: instance is nil")
	}

	// 6. Resource ownership consumer DI (Shared Redis Stream, Central-internal)
	ownershipRepo := repository.NewResourceOwnershipRepository(dbPool)
	if ownershipRepo == nil {
		return nil, fmt.Errorf("failed to initialize ResourceOwnershipRepository: instance is nil")
	}

	ownershipService := service.NewResourceOwnershipService(ownershipRepo)
	if ownershipService == nil {
		return nil, fmt.Errorf("failed to initialize ResourceOwnershipService: instance is nil")
	}

	ownershipConsumer, err := redisHandler.NewResourceOwnershipConsumer(redisClient, ownershipService)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize ResourceOwnershipConsumer: %w", err)
	}
	if ownershipConsumer == nil {
		return nil, fmt.Errorf("failed to initialize ResourceOwnershipConsumer: instance is nil")
	}
	// Initialize authorization after transport consumers so a partial module
	// construction cannot expose HTTP with an incomplete security resolver.
	authorizationResolver, err := service.NewAuthorizationResolver(authRedisClient, redisClient)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize AuthorizationResolver: %w", err)
	}

	return &Module{
		AccountRepo:                     accountRepo,
		AccountService:                  accountService,
		AccountHandler:                  accountHandler,
		PersonalWalletProvisionConsumer: personalWalletProvisionConsumer,
		PlanRepo:                        planRepo,
		PlanService:                     planService,
		PlanHandler:                     planHandler,
		TierRepo:                        tierRepo,
		TierService:                     tierService,
		TierHandler:                     tierHandler,
		ReconcilerRepo:                  reconcilerRepo,
		ReconcilerService:               reconcilerService,
		ReconcilerWorker:                reconcilerWorker,
		ResourceOwnershipRepo:           ownershipRepo,
		ResourceOwnershipService:        ownershipService,
		ResourceOwnershipConsumer:       ownershipConsumer,
		PricingOutboxRepo:               pricingOutboxRepo,
		PricingOutboxRelay:              pricingOutboxRelay,
		AuthorizationResolver:           authorizationResolver,
	}, nil
}
