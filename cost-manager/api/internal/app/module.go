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
	natsHandler "cost-manager/api/internal/transport/nats/handler"
	"cost-manager/api/internal/transport/rpc"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
)

// [COMMENT]: Module quản lý tất cả các repository, service và handler của ứng dụng.
type Module struct {
	AccountRepo                       billingRepoInterface.AccountRepository
	AccountService                    billingSvcInterface.AccountService
	AccountHandler                    *handler.AccountHandler
	PersonalWalletProvisionSubscriber *natsHandler.PersonalWalletProvisionSubscriber

	PlanRepo    billingRepoInterface.PlanRepository
	PlanService billingSvcInterface.PlanService
	PlanHandler *handler.PlanHandler

	TierRepo    billingRepoInterface.TierRepository
	TierService billingSvcInterface.TierService
	TierHandler *handler.TierHandler

	ReconcilerRepo    billingRepoInterface.ReconcilerRepository
	ReconcilerService service.ReconcilerService
	ReconcilerWorker  *rpc.StorageOwnershipReconcilerWorker

	ResourceOwnershipRepo       billingRepoInterface.ResourceOwnershipRepository
	ResourceOwnershipService    service.ResourceOwnershipService
	ResourceOwnershipSubscriber *natsHandler.ResourceOwnershipSubscriber

	PricingOutboxRepo  billingRepoInterface.PricingOutboxRepository
	PricingOutboxRelay *service.PricingOutboxRelay
}

// [COMMENT]: NewModule khởi tạo Module và thực hiện Dependency Injection kèm nil check đầy đủ sau mỗi bước.
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

	// 1. Account Domain DI
	accountRepo := repository.NewAccountRepository(dbPool)
	if accountRepo == nil {
		return nil, fmt.Errorf("failed to initialize AccountRepository: instance is nil")
	}

	accountService := service.NewAccountService(accountRepo)
	if accountService == nil {
		return nil, fmt.Errorf("failed to initialize AccountService: instance is nil")
	}

	accountHandler := handler.NewAccountHandler(accountService)
	if accountHandler == nil {
		return nil, fmt.Errorf("failed to initialize AccountHandler: instance is nil")
	}
	personalWalletProvisionSubscriber, err := natsHandler.NewPersonalWalletProvisionSubscriber(natsConn, accountService)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize PersonalWalletProvisionSubscriber: %w", err)
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

	// 3. Tier Domain DI
	tierRepo := repository.NewTierRepository(dbPool)
	if tierRepo == nil {
		return nil, fmt.Errorf("failed to initialize TierRepository: instance is nil")
	}

	tierService := service.NewTierService(tierRepo)
	if tierService == nil {
		return nil, fmt.Errorf("failed to initialize TierService: instance is nil")
	}

	tierHandler := handler.NewTierHandler(tierService)
	if tierHandler == nil {
		return nil, fmt.Errorf("failed to initialize TierHandler: instance is nil")
	}

	// 4. Auth Domain DI & NATS Subscription
	authRepo := repository.NewAuthRepository(dbPool)
	if authRepo == nil {
		return nil, fmt.Errorf("failed to initialize AuthRepository: instance is nil")
	}

	authService := service.NewAuthService(authRepo)
	if authService == nil {
		return nil, fmt.Errorf("failed to initialize AuthService: instance is nil")
	}

	if _, err := natsHandler.SubscribeAuth(natsConn, authService); err != nil {
		return nil, fmt.Errorf("failed to register NATS auth subscriber: %w", err)
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

	// 6. Resource ownership subscriber DI (NATS JetStream)
	ownershipRepo := repository.NewResourceOwnershipRepository(dbPool)
	if ownershipRepo == nil {
		return nil, fmt.Errorf("failed to initialize ResourceOwnershipRepository: instance is nil")
	}

	ownershipService := service.NewResourceOwnershipService(ownershipRepo)
	if ownershipService == nil {
		return nil, fmt.Errorf("failed to initialize ResourceOwnershipService: instance is nil")
	}

	ownershipSubscriber, err := natsHandler.NewResourceOwnershipSubscriber(natsConn, ownershipService)
	if err != nil || ownershipSubscriber == nil {
		return nil, fmt.Errorf("failed to initialize ResourceOwnershipSubscriber: %w", err)
	}

	// 7. Pricing Outbox Relay DI
	pricingOutboxRepo := repository.NewPricingOutboxRepository(dbPool)
	if pricingOutboxRepo == nil {
		return nil, fmt.Errorf("failed to initialize PricingOutboxRepository: instance is nil")
	}

	pricingOutboxRelay := service.NewPricingOutboxRelay(pricingOutboxRepo, natsConn)
	if pricingOutboxRelay == nil {
		return nil, fmt.Errorf("failed to initialize PricingOutboxRelay: instance is nil")
	}

	return &Module{
		AccountRepo:                       accountRepo,
		AccountService:                    accountService,
		AccountHandler:                    accountHandler,
		PersonalWalletProvisionSubscriber: personalWalletProvisionSubscriber,
		PlanRepo:                          planRepo,
		PlanService:                       planService,
		PlanHandler:                       planHandler,
		TierRepo:                          tierRepo,
		TierService:                       tierService,
		TierHandler:                       tierHandler,
		ReconcilerRepo:                    reconcilerRepo,
		ReconcilerService:                 reconcilerService,
		ReconcilerWorker:                  reconcilerWorker,
		ResourceOwnershipRepo:             ownershipRepo,
		ResourceOwnershipService:          ownershipService,
		ResourceOwnershipSubscriber:       ownershipSubscriber,
		PricingOutboxRepo:                 pricingOutboxRepo,
		PricingOutboxRelay:                pricingOutboxRelay,
	}, nil
}
