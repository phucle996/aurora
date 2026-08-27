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
	"cost-manager/api/internal/transport/middleware"
	redisHandler "cost-manager/api/internal/transport/redis/handler"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Module quản lý tất cả các repository, service và handler của ứng dụng Cost Manager.
type Module struct {
	PersonalAccountRepo             billingRepoInterface.PersonalAccountRepository
	TenantAccountRepo               billingRepoInterface.TenantAccountRepository
	PersonalAccountService          billingSvcInterface.PersonalAccountService
	TenantAccountService            billingSvcInterface.TenantAccountService
	PersonalAccountHandler          *handler.PersonalAccountHandler
	PersonalWalletProvisionConsumer *redisHandler.PersonalWalletProvisionConsumer
	TenantWalletProvisionConsumer   *redisHandler.TenantWalletProvisionConsumer

	PersonalPaymentRepo    billingRepoInterface.PersonalPaymentRepository
	TenantPaymentRepo      billingRepoInterface.TenantPaymentRepository
	PersonalPaymentService billingSvcInterface.PersonalPaymentService
	TenantPaymentService   billingSvcInterface.TenantPaymentService
	PersonalPaymentHandler *handler.PersonalPaymentHandler
	TenantPaymentHandler   *handler.TenantPaymentHandler

	StoragePricingService  billingSvcInterface.StoragePricingService
	StoragePricingHandler  *handler.StoragePricingHandler
	PricingScheduleHandler *handler.PricingScheduleHandler

	HypervisorPricingService      billingSvcInterface.HypervisorPricingService
	HypervisorPricingHandler      *handler.HypervisorPricingHandler
	HypervisorResourcePlanService billingSvcInterface.HypervisorResourcePlanService
	HypervisorResourcePlanHandler *handler.HypervisorResourcePlanHandler
	MailPricingService            billingSvcInterface.MailPricingService
	MailPricingHandler            *handler.MailPricingHandler

	ResourceOwnershipRepo     billingRepoInterface.ResourceOwnershipRepository
	ResourceOwnershipService  billingSvcInterface.ResourceOwnershipService
	ResourceOwnershipConsumer *redisHandler.ResourceOwnershipConsumer

	PricingScheduleService billingSvcInterface.PricingScheduleService

	WalletAdmissionOutboxRepo  billingRepoInterface.WalletAdmissionOutboxRepository
	WalletAdmissionOutboxRelay *service.WalletAdmissionOutboxRelay

	PersonalAuthorizationMiddleware *middleware.PersonalAuthorizationMiddleware
	TenantAuthorizationMiddleware   *middleware.TenantAuthorizationMiddleware
}

// NewModule khởi tạo Module và thực hiện Dependency Injection kèm nil check đầy đủ sau mỗi bước.
func NewModule(
	dbPool *pgxpool.Pool,
	redisClient *redis.Client,
	authRedisClient *redis.Client,
	paymentCfg config.PaymentCfg,
	resourcePlanRedis redis.UniversalClient,
	relayCfg config.ResourcePlanRelayCfg,
	walletAdmissionRelayCfg config.WalletAdmissionRelayCfg,
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

	// 1. Account DI
	personalAccountRepo := repository.NewPersonalAccountRepository(dbPool)
	if personalAccountRepo == nil {
		return nil, fmt.Errorf("failed to initialize PersonalAccountRepository: instance is nil")
	}
	tenantAccountRepo := repository.NewTenantAccountRepository(dbPool)
	if tenantAccountRepo == nil {
		return nil, fmt.Errorf("failed to initialize TenantAccountRepository: instance is nil")
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
	personalAccountService := service.NewPersonalAccountService(personalAccountRepo, paymentPolicy)
	tenantAccountService := service.NewTenantAccountService(tenantAccountRepo)

	personalAccountHandler := handler.NewPersonalAccountHandler(
		personalAccountService,
		paymentPolicy.MinimumTopUp,
	)
	if personalAccountHandler == nil {
		return nil, fmt.Errorf("failed to initialize PersonalAccountHandler: instance is nil")
	}
	personalWalletProvisionConsumer := redisHandler.NewPersonalWalletProvisionConsumer(
		redisClient,
		personalAccountService,
	)
	if personalWalletProvisionConsumer == nil {
		return nil, fmt.Errorf("failed to initialize PersonalWalletProvisionConsumer: instance is nil")
	}
	tenantWalletProvisionConsumer := redisHandler.NewTenantWalletProvisionConsumer(
		redisClient,
		tenantAccountService,
	)
	if tenantWalletProvisionConsumer == nil {
		return nil, fmt.Errorf("failed to initialize TenantWalletProvisionConsumer: instance is nil")
	}

	// 2. Payment DI
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

	// 3. Admission Outbox DI
	walletAdmissionOutboxRepo := repository.NewWalletAdmissionOutboxRepository(dbPool)
	if walletAdmissionOutboxRepo == nil {
		return nil, fmt.Errorf("failed to initialize WalletAdmissionOutboxRepository: instance is nil")
	}
	if walletAdmissionRelayCfg.ReplicaAcks < 0 || walletAdmissionRelayCfg.DurableWait < time.Millisecond || walletAdmissionRelayCfg.DurableWait > 5*time.Second {
		return nil, fmt.Errorf("invalid wallet admission durability policy")
	}
	walletAdmissionOutboxRelay := service.NewWalletAdmissionOutboxRelay(
		walletAdmissionOutboxRepo,
		redisClient,
		entity.WalletAdmissionRelayPolicy{
			ReplicaAcks: walletAdmissionRelayCfg.ReplicaAcks,
			DurableWait: walletAdmissionRelayCfg.DurableWait,
		},
	)
	if walletAdmissionOutboxRelay == nil {
		return nil, fmt.Errorf("failed to initialize WalletAdmissionOutboxRelay: instance is nil")
	}

	// 4. Pricing Schedule & Storage Pricing DI
	pricingScheduleRepo := repository.NewPricingScheduleRepository(dbPool)
	if pricingScheduleRepo == nil {
		return nil, fmt.Errorf("failed to initialize PricingScheduleRepository: instance is nil")
	}

	storagePricingRepo := repository.NewStoragePricingRepository(dbPool)
	if storagePricingRepo == nil {
		return nil, fmt.Errorf("failed to initialize StoragePricingRepository: instance is nil")
	}

	pricingScheduleService := service.NewPricingScheduleService(pricingScheduleRepo)
	if pricingScheduleService == nil {
		return nil, fmt.Errorf("failed to initialize PricingScheduleService: instance is nil")
	}
	storagePricingService := service.NewStoragePricingService(storagePricingRepo, redisClient)
	if storagePricingService == nil {
		return nil, fmt.Errorf("failed to initialize StoragePricingService: instance is nil")
	}

	storagePricingHandler := handler.NewStoragePricingHandler(storagePricingService)
	if storagePricingHandler == nil {
		return nil, fmt.Errorf("failed to initialize StoragePricingHandler: instance is nil")
	}

	pricingScheduleHandler := handler.NewPricingScheduleHandler(pricingScheduleService)
	if pricingScheduleHandler == nil {
		return nil, fmt.Errorf("failed to initialize PricingScheduleHandler: instance is nil")
	}

	// 5. Hypervisor & Mail Pricing DI
	hypervisorPricingRepo := repository.NewHypervisorPricingRepository(dbPool)
	if hypervisorPricingRepo == nil {
		return nil, fmt.Errorf("failed to initialize HypervisorPricingRepository: instance is nil")
	}
	hypervisorPricingService := service.NewHypervisorPricingService(hypervisorPricingRepo, redisClient)
	if hypervisorPricingService == nil {
		return nil, fmt.Errorf("failed to initialize HypervisorPricingService: instance is nil")
	}
	hypervisorPricingHandler := handler.NewHypervisorPricingHandler(hypervisorPricingService)
	if hypervisorPricingHandler == nil {
		return nil, fmt.Errorf("failed to initialize HypervisorPricingHandler: instance is nil")
	}
	if relayCfg.ReplicaAcks < 0 || relayCfg.DurableWait < time.Millisecond || relayCfg.DurableWait > 5*time.Second {
		return nil, fmt.Errorf("invalid Hypervisor resource plan durability policy")
	}
	hypervisorResourcePlanRepo := repository.NewHypervisorResourcePlanRepository(dbPool)
	if hypervisorResourcePlanRepo == nil {
		return nil, fmt.Errorf("failed to initialize HypervisorResourcePlanRepository: instance is nil")
	}
	hypervisorResourcePlanService := service.NewHypervisorResourcePlanService(hypervisorResourcePlanRepo, resourcePlanRedis, entity.HypervisorResourcePlanRelayPolicy{ReplicaAcks: relayCfg.ReplicaAcks, DurableWait: relayCfg.DurableWait})
	if hypervisorResourcePlanService == nil {
		return nil, fmt.Errorf("failed to initialize HypervisorResourcePlanService: instance is nil")
	}
	hypervisorResourcePlanHandler := handler.NewHypervisorResourcePlanHandler(hypervisorResourcePlanService)
	if hypervisorResourcePlanHandler == nil {
		return nil, fmt.Errorf("failed to initialize HypervisorResourcePlanHandler: instance is nil")
	}

	mailPricingRepo := repository.NewMailPricingRepository(dbPool)
	if mailPricingRepo == nil {
		return nil, fmt.Errorf("failed to initialize MailPricingRepository: instance is nil")
	}
	mailPricingService := service.NewMailPricingService(mailPricingRepo, redisClient)
	if mailPricingService == nil {
		return nil, fmt.Errorf("failed to initialize MailPricingService: instance is nil")
	}
	mailPricingHandler := handler.NewMailPricingHandler(mailPricingService)
	if mailPricingHandler == nil {
		return nil, fmt.Errorf("failed to initialize MailPricingHandler: instance is nil")
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

	ownershipConsumer := redisHandler.NewResourceOwnershipConsumer(redisClient, ownershipService)
	if ownershipConsumer == nil {
		return nil, fmt.Errorf("failed to initialize ResourceOwnershipConsumer: instance is nil")
	}

	// 7. Authorization Middlewares
	personalAuthorizationMiddleware, err := middleware.NewPersonalAuthorizationMiddleware(authRedisClient, redisClient)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize PersonalAuthorizationMiddleware: %w", err)
	}
	tenantAuthorizationMiddleware, err := middleware.NewTenantAuthorizationMiddleware(authRedisClient, redisClient)
	if err != nil {
		personalAuthorizationMiddleware.Close()
		return nil, fmt.Errorf("failed to initialize TenantAuthorizationMiddleware: %w", err)
	}

	return &Module{
		PersonalAccountRepo:             personalAccountRepo,
		TenantAccountRepo:               tenantAccountRepo,
		PersonalAccountService:          personalAccountService,
		TenantAccountService:            tenantAccountService,
		PersonalAccountHandler:          personalAccountHandler,
		PersonalWalletProvisionConsumer: personalWalletProvisionConsumer,
		TenantWalletProvisionConsumer:   tenantWalletProvisionConsumer,
		PersonalPaymentRepo:             personalPaymentRepository,
		TenantPaymentRepo:               tenantPaymentRepository,
		PersonalPaymentService:          personalPaymentService,
		TenantPaymentService:            tenantPaymentService,
		PersonalPaymentHandler:          personalPaymentHandler,
		TenantPaymentHandler:            tenantPaymentHandler,
		StoragePricingService:           storagePricingService,
		StoragePricingHandler:           storagePricingHandler,
		PricingScheduleHandler:          pricingScheduleHandler,
		HypervisorPricingService:        hypervisorPricingService,
		HypervisorPricingHandler:        hypervisorPricingHandler,
		HypervisorResourcePlanService:   hypervisorResourcePlanService,
		HypervisorResourcePlanHandler:   hypervisorResourcePlanHandler,
		MailPricingService:              mailPricingService,
		MailPricingHandler:              mailPricingHandler,
		ResourceOwnershipRepo:           ownershipRepo,
		ResourceOwnershipService:        ownershipService,
		ResourceOwnershipConsumer:       ownershipConsumer,
		PricingScheduleService:          pricingScheduleService,
		WalletAdmissionOutboxRepo:       walletAdmissionOutboxRepo,
		WalletAdmissionOutboxRelay:      walletAdmissionOutboxRelay,
		PersonalAuthorizationMiddleware: personalAuthorizationMiddleware,
		TenantAuthorizationMiddleware:   tenantAuthorizationMiddleware,
	}, nil
}
