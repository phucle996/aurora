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
	"net/url"
	"strings"
	"time"

	"cost-manager/api/internal/config"
	"cost-manager/api/internal/domain/entity"
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
	TenantWalletProvisionConsumer   *redisHandler.TenantWalletProvisionConsumer

	PersonalPaymentRepo    billingRepoInterface.PersonalPaymentRepository
	TenantPaymentRepo      billingRepoInterface.TenantPaymentRepository
	PersonalPaymentService billingSvcInterface.PersonalPaymentService
	TenantPaymentService   billingSvcInterface.TenantPaymentService
	PersonalPaymentHandler *handler.PersonalPaymentHandler
	TenantPaymentHandler   *handler.TenantPaymentHandler

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
	paymentCfg config.PaymentCfg,
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

	intentTTL, err := time.ParseDuration(paymentCfg.IntentTTL)
	if err != nil {
		return nil, fmt.Errorf("invalid PAYMENT_INTENT_TTL: %w", err)
	}
	referralTTL, err := time.ParseDuration(paymentCfg.ReferralReservationTTL)
	if err != nil {
		return nil, fmt.Errorf("invalid PAYMENT_REFERRAL_RESERVATION_TTL: %w", err)
	}
	webhookTolerance, err := time.ParseDuration(paymentCfg.WebhookTolerance)
	if err != nil {
		return nil, fmt.Errorf("invalid PAYMENT_WEBHOOK_TOLERANCE: %w", err)
	}
	paymentPolicy := entity.PaymentPolicy{
		Provider:           paymentCfg.Provider,
		CheckoutBaseURL:    paymentCfg.CheckoutBaseURL,
		ReturnBaseURL:      paymentCfg.ReturnBaseURL,
		CheckoutSigningKey: paymentCfg.CheckoutSigningSecret,
		WebhookSigningKey:  paymentCfg.WebhookSigningSecret,
		MinimumTopUp:       paymentCfg.MinimumTopUpMicroUnits,
		IntentTTL:          intentTTL,
		ReferralTTL:        referralTTL,
		WebhookTolerance:   webhookTolerance,
	}
	checkoutURL, checkoutURLErr := url.Parse(paymentPolicy.CheckoutBaseURL)
	if checkoutURLErr != nil || checkoutURL.Scheme != "https" || checkoutURL.Host == "" {
		return nil, fmt.Errorf("PAYMENT_CHECKOUT_BASE_URL must be an absolute HTTPS URL")
	}
	returnURL, returnURLErr := url.Parse(paymentPolicy.ReturnBaseURL)
	if returnURLErr != nil || returnURL.Scheme != "https" || returnURL.Host == "" {
		return nil, fmt.Errorf("PAYMENT_RETURN_BASE_URL must be an absolute HTTPS URL")
	}
	if strings.TrimSpace(paymentPolicy.Provider) == "" ||
		len(paymentPolicy.CheckoutSigningKey) < 32 ||
		len(paymentPolicy.WebhookSigningKey) < 32 ||
		paymentPolicy.CheckoutSigningKey == paymentPolicy.WebhookSigningKey ||
		paymentPolicy.MinimumTopUp <= 0 ||
		paymentPolicy.IntentTTL <= 0 ||
		paymentPolicy.ReferralTTL <= 0 ||
		paymentPolicy.WebhookTolerance <= 0 {
		return nil, fmt.Errorf("payment configuration is incomplete or uses weak signing keys")
	}
	accountService := service.NewAccountService(accountRepo, paymentPolicy)

	accountHandler := handler.NewAccountHandler(accountService, paymentPolicy.MinimumTopUp)
	if accountHandler == nil {
		return nil, fmt.Errorf("failed to initialize AccountHandler: instance is nil")
	}
	personalWalletProvisionConsumer, err := redisHandler.NewPersonalWalletProvisionConsumer(redisClient, accountService)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize PersonalWalletProvisionConsumer: %w", err)
	}
	tenantWalletProvisionConsumer, err := redisHandler.NewTenantWalletProvisionConsumer(redisClient, accountService)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize TenantWalletProvisionConsumer: %w", err)
	}

	personalPaymentRepository := repository.NewPersonalPaymentRepository(dbPool)
	tenantPaymentRepository := repository.NewTenantPaymentRepository(dbPool)
	personalPaymentService := service.NewPersonalPaymentService(
		personalPaymentRepository,
		redisClient,
		paymentPolicy,
		*checkoutURL,
		*returnURL,
	)
	tenantPaymentService := service.NewTenantPaymentService(
		tenantPaymentRepository,
		redisClient,
		paymentPolicy,
		*checkoutURL,
		*returnURL,
	)
	personalPaymentHandler := handler.NewPersonalPaymentHandler(personalPaymentService, paymentPolicy)
	tenantPaymentHandler := handler.NewTenantPaymentHandler(tenantPaymentService, paymentPolicy)

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

	tierService := service.NewTierService(tierRepo, redisClient, pricingOutboxRelay.Notify)
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
		TenantWalletProvisionConsumer:   tenantWalletProvisionConsumer,
		PersonalPaymentRepo:             personalPaymentRepository,
		TenantPaymentRepo:               tenantPaymentRepository,
		PersonalPaymentService:          personalPaymentService,
		TenantPaymentService:            tenantPaymentService,
		PersonalPaymentHandler:          personalPaymentHandler,
		TenantPaymentHandler:            tenantPaymentHandler,
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
