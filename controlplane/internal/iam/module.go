package iam

import (
	kafkainfra "controlplane/infra/kafka"
	vaultinfra "controlplane/infra/vault"
	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamRepoImpl "controlplane/internal/iam/repository"
	iamSvcImpl "controlplane/internal/iam/service"
	iamHandler "controlplane/internal/iam/transport/http/handler"
	iamPubsub "controlplane/internal/iam/transport/pubsub"
	iamPubsubHandler "controlplane/internal/iam/transport/pubsub/handler"
	"controlplane/internal/observability"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

type IAMModule struct {
	cfg        *config.Config
	db         *pgxpool.Pool
	rds        *goredis.Client
	authRedis  *goredis.Client
	L1Registry *cacheengine.CacheRegistry
	otel       *observability.OTel

	// HTTP Transport Handlers (Exposed to the router in API gateway layer)
	AuthHandler           *iamHandler.AuthHandler
	UserHandler           *iamHandler.UserHandler
	SelfDeviceHandler     *iamHandler.SelfDeviceHandler     // [COMMENT]: Handler nhánh self
	PersonalDeviceHandler *iamHandler.PersonalDeviceHandler // [COMMENT]: Handler nhánh personal
	RbacPlatformHandler   *iamHandler.RbacPlatformHandler   // [COMMENT]: Handler cho các tác vụ platform-scoped RBAC
	RbacTenantHandler     *iamHandler.RbacTenantHandler     // [COMMENT]: Handler cho các tác vụ tenant-scoped RBAC
	RenderContextHandler  *iamHandler.RenderContextHandler
	MfaHandler            *iamHandler.MfaHandler // [COMMENT]: Handler phục vụ tra cứu thông tin MFA platform audit

	// Core Services & Sync Engines
	RbacPlatformRepository               iamRepoInterface.RbacPlatformRepository // [COMMENT]: Repo quản lý platform role
	RbacTenantRepository                 iamRepoInterface.RbacTenantRepository   // [COMMENT]: Repo quản lý tenant role
	RenderContextRepository              iamRepoInterface.RenderContextRepository
	SelfDeviceRepository                 iamRepoInterface.SelfDeviceRepository     // [COMMENT]: Repo thiết bị của verified self user
	PersonalDeviceRepository             iamRepoInterface.PersonalDeviceRepository // [COMMENT]: Repo nhánh personal quản lý thiết bị platform
	AuthService                          iamSvcInterface.AuthService
	UserService                          iamSvcInterface.UserService
	SessionRefreshService                iamSvcInterface.SessionRefreshService
	selfDeviceSvcImpl                    iamSvcInterface.SelfDeviceService     // giữ interface type để tránh type assertion
	personalDeviceSvcImpl                iamSvcInterface.PersonalDeviceService // giữ interface type để tránh type assertion
	lifecycleFactRelay                   *iamSvcImpl.LifecycleFactRelay
	billingAuthorizationRedisHandler     *iamPubsubHandler.BillingAuthorizationRedisHandler
	runtimeReadAuthorizationRedisHandler *iamPubsubHandler.RuntimeReadAuthorizationRedisHandler
	authRedisHandler                     *iamPubsubHandler.AuthRedisHandler
	devicePresenceProjectionHandler      *iamPubsubHandler.DevicePresenceProjectionRedisHandler
	deviceSessionCapacityEvictionHandler *iamPubsubHandler.DeviceSessionCapacityEvictionRedisHandler
	tenantAccessRedisHandler             *iamPubsubHandler.TenantAccessRedisHandler
	personalAccessRedisHandler           *iamPubsubHandler.PersonalAccessRedisHandler
}

func (m *IAMModule) NotifyLifecycleFactOutbox() {
	if m == nil || m.lifecycleFactRelay == nil {
		return
	}
	m.lifecycleFactRelay.Notify()
}

// NewModule khởi tạo phân hệ IAM. Thiết lập cơ chế Fail-Fast chặt chẽ ở cấp độ biên khởi chạy.
func NewModule(
	cfg *config.Config,
	db *pgxpool.Pool,
	rds *goredis.Client,
	authRedis *goredis.Client,
	vaultClient *vaultinfra.Client,
	kafkaProducer *kafkainfra.Producer,
	cacheEngine *cacheengine.CacheRegistry,
	otel *observability.OTel,
) (*IAMModule, error) {

	// ------------------------------------------------------------------------
	// 🛑 GIAI ĐOẠN 1: STRICT INITIALIZATION CHECK (FAIL-FAST POLICY)
	// ------------------------------------------------------------------------
	// SRE NOTE: Nếu một trong các tài nguyên hệ thống cơ bản bị nil, tiến trình pod
	// phải bị crash/terminating ngay từ đầu để Kubernetes điều phối viên (Scheduler)
	// nhận biết và báo trạng thái CrashLoopBackOff sớm.

	if cfg == nil {
		return nil, errors.New("iam module: configuration block (cfg) is nil (check configmap/secrets mount)")
	}
	if db == nil {
		return nil, errors.New("iam module: postgresql database pool (db) is nil (check pg_bouncer or connections)")
	}
	if rds == nil {
		return nil, errors.New("iam module: redis cluster client (rds) is nil (check redis sentinel/cluster endpoint)")
	}
	if authRedis == nil {
		return nil, errors.New("iam module: auth redis client is nil")
	}
	if vaultClient == nil {
		return nil, errors.New("iam module: vault client is nil")
	}
	if kafkaProducer == nil {
		return nil, errors.New("iam module: kafka producer is nil")
	}

	if cacheEngine == nil {
		return nil, errors.New("iam module: cache engine is nil")
	}
	if otel == nil {
		return nil, errors.New("iam module: observability is nil")
	}

	// ------------------------------------------------------------------------
	// 🔄 GIAI ĐOẠN 2: CORE REPOSITORIES & CACHES BOOTSTRAPPING
	// ------------------------------------------------------------------------
	// Khởi tạo các adapter tương tác dữ liệu và cấu trúc lưu trữ cache cục bộ/Redis.

	// Auth Repository (PostgreSQL)
	authRepo := iamRepoImpl.NewAuthRepository(cfg, db)
	if authRepo == nil {
		return nil, errors.New("iam module: failed to construct auth repository (database mismatch)")
	}

	// User Repository (PostgreSQL)
	userRepo := iamRepoImpl.NewUserRepository(cfg, db)
	if userRepo == nil {
		return nil, errors.New("iam module: failed to construct user repository")
	}

	// Self Device Repository (PostgreSQL)
	selfDeviceRepo := iamRepoImpl.NewSelfDeviceRepository(cfg, db)
	if selfDeviceRepo == nil {
		return nil, errors.New("iam module: failed to construct self device repository")
	}

	// Personal Device Repository (PostgreSQL)
	personalDeviceRepo := iamRepoImpl.NewPersonalDeviceRepository(cfg, db)
	if personalDeviceRepo == nil {
		return nil, errors.New("iam module: failed to construct personal device repository")
	}

	// Refresh Token Storage (PostgreSQL)
	refreshTokenRepo := iamRepoImpl.NewRefreshTokenRepository(cfg.SchemaSQL, db)
	if refreshTokenRepo == nil {
		return nil, errors.New("iam module: failed to construct refresh token repository")
	}

	// [COMMENT]: Khởi tạo các repository platform/tenant RBAC sớm phục vụ DI
	rbacPlatformRepo := iamRepoImpl.NewRbacPlatformRepository(cfg, db)
	if rbacPlatformRepo == nil {
		return nil, errors.New("iam module: failed to construct RBAC platform repository")
	}

	rbacTenantRepo := iamRepoImpl.NewRbacTenantRepository(cfg, db)
	if rbacTenantRepo == nil {
		return nil, errors.New("iam module: failed to construct RBAC tenant repository")
	}
	renderContextRepo := iamRepoImpl.NewRenderContextRepository(cacheEngine)
	if renderContextRepo == nil {
		return nil, errors.New("iam module: failed to construct render context repository")
	}
	billingAuthorizationRedisHandler, err := iamPubsubHandler.NewBillingAuthorizationRedisHandler(
		rds,
		authRedis,
		cacheEngine,
	)
	if err != nil {
		return nil, err
	}
	personalRuntimeReadAuthorizationRepo := iamRepoImpl.NewPersonalRuntimeReadAuthorizationRepository(cacheEngine)
	personalRuntimeReadAuthorizationSvc := iamSvcImpl.NewPersonalRuntimeReadAuthorizationService(personalRuntimeReadAuthorizationRepo)
	tenantRuntimeReadAuthorizationRepo := iamRepoImpl.NewTenantRuntimeReadAuthorizationRepository(cacheEngine)
	tenantRuntimeReadAuthorizationSvc := iamSvcImpl.NewTenantRuntimeReadAuthorizationService(tenantRuntimeReadAuthorizationRepo)
	runtimeReadAuthorizationRedisHandler, err := iamPubsubHandler.NewRuntimeReadAuthorizationRedisHandler(
		rds,
		personalRuntimeReadAuthorizationSvc,
		tenantRuntimeReadAuthorizationSvc,
	)
	if err != nil {
		return nil, err
	}

	// One Time Token Service
	oneTimeTokenSvc := iamSvcImpl.NewOneTimeTokenService(cfg, cacheEngine)
	if oneTimeTokenSvc == nil {
		return nil, errors.New("iam module: failed to construct one time token service")
	}

	// ------------------------------------------------------------------------
	// 💼 GIAI ĐOẠN 3: SERVICE LAYER INITIALIZATION
	// ------------------------------------------------------------------------
	// Khởi tạo các Engine xử lý Business Logic chính.
	workflowMetrics := otel.WorkflowRecorder("iam")

	selfDeviceSvc := iamSvcImpl.NewSelfDeviceService(selfDeviceRepo, cacheEngine, rds, authRedis, workflowMetrics)
	if selfDeviceSvc == nil {
		return nil, errors.New("iam module: failed to construct self device service")
	}

	devicePresenceProjectionHandler := iamPubsubHandler.NewDevicePresenceProjectionRedisHandler(
		rds,
		selfDeviceSvc,
		otel,
	)
	deviceSessionCapacityEvictionHandler := iamPubsubHandler.NewDeviceSessionCapacityEvictionRedisHandler(
		rds,
		selfDeviceSvc,
		otel,
	)

	personalDeviceSvc := iamSvcImpl.NewPersonalDeviceService(personalDeviceRepo, workflowMetrics)
	if personalDeviceSvc == nil {
		return nil, errors.New("iam module: failed to construct personal device service")
	}

	selfDeviceHandler := iamHandler.NewSelfDeviceHandler(selfDeviceSvc)
	if selfDeviceHandler == nil {
		return nil, errors.New("iam module: failed to initialize HTTP self device handler")
	}
	personalDeviceHandler := iamHandler.NewPersonalDeviceHandler(personalDeviceSvc)
	if personalDeviceHandler == nil {
		return nil, errors.New("iam module: failed to initialize HTTP personal device handler")
	}

	// ------------------------------------------------------------------------
	// 🛡️ GIAI ĐOẠN 5: Platform & Tenant RBAC Repos Bootstrapping (giải quyết DI)
	// ------------------------------------------------------------------------

	refreshSvc := iamSvcImpl.NewSessionRefreshService(cfg, refreshTokenRepo, workflowMetrics)
	if refreshSvc == nil {
		return nil, errors.New("iam module: failed to construct session refresh service")
	}

	lifecycleFactRepo := iamRepoImpl.NewLifecycleFactOutboxRepository(db, cfg)
	lifecycleFactRelay, err := iamSvcImpl.NewLifecycleFactRelay(
		lifecycleFactRepo,
		rds,
		cfg.Redis.DurableReplicaAcks,
		cfg.Redis.DurableWait,
	)
	if err != nil {
		return nil, err
	}
	verificationPublisher, err := iamPubsub.NewAccountVerificationPublisher(
		kafkaProducer,
		cfg.Kafka.IAMVerificationTopic,
	)
	if err != nil {
		return nil, err
	}

	// [COMMENT]: Khởi tạo MFA repository chịu trách nhiệm thao tác dữ liệu DB và kiểm tra nil
	mfaRepo := iamRepoImpl.NewMfaRepository(cfg, db)
	if mfaRepo == nil {
		return nil, errors.New("iam module: failed to construct mfa repository implementation")
	}

	// [COMMENT]: Khởi tạo MFA service chịu trách nhiệm xử lý business logic MFA và kiểm tra nil
	mfaSvc := iamSvcImpl.NewMfaService(vaultClient, mfaRepo, authRedis, workflowMetrics)
	if mfaSvc == nil {
		return nil, errors.New("iam module: failed to construct mfa service implementation")
	}

	// [COMMENT]: Khởi tạo HTTP MFA handler chịu trách nhiệm xử lý API request và kiểm tra nil
	mfaHandler := iamHandler.NewMfaHandler(mfaSvc)
	if mfaHandler == nil {
		return nil, errors.New("iam module: failed to initialize HTTP mfa handler")
	}

	authSvc := iamSvcImpl.NewAuthService(
		authRepo, refreshSvc, selfDeviceSvc,
		cacheEngine, oneTimeTokenSvc, verificationPublisher,
		lifecycleFactRelay, mfaSvc,
		nil,
		workflowMetrics,
	)
	if authSvc == nil {
		return nil, errors.New("iam module: failed to construct core auth service implementation")
	}

	userService := iamSvcImpl.NewUserService(userRepo, cacheEngine, authRedis, rds, workflowMetrics)
	if userService == nil {
		return nil, errors.New("iam module: failed to construct core user service implementation")
	}

	authRedisHandler, err := iamPubsubHandler.NewAuthRedisHandler(
		cfg,
		rds,
		authSvc,
		userService,
		refreshSvc,
		otel,
	)
	if err != nil {
		return nil, fmt.Errorf("iam module: failed to initialize Auth Redis handler: %w", err)
	}

	authHandler := iamHandler.NewAuthHandler(cfg, authSvc)
	if authHandler == nil {
		return nil, errors.New("iam module: failed to initialize HTTP auth handler")
	}

	userHandler := iamHandler.NewUserHandler(userService)
	if userHandler == nil {
		return nil, errors.New("iam module: failed to initialize HTTP user handler")
	}

	// [COMMENT]: Khởi tạo các service quản lý luồng nghiệp vụ platform/tenant RBAC
	rbacPlatformSvc := iamSvcImpl.NewRbacPlatformService(
		rbacPlatformRepo,
		cacheEngine,
		authRedis,
		rds,
		workflowMetrics,
	)
	if rbacPlatformSvc == nil {
		return nil, errors.New("iam module: failed to construct RBAC platform service")
	}

	rbacTenantSvc := iamSvcImpl.NewRbacTenantService(rbacTenantRepo, workflowMetrics)
	if rbacTenantSvc == nil {
		return nil, errors.New("iam module: failed to construct RBAC tenant service")
	}
	renderContextSvc := iamSvcImpl.NewRenderContextService(renderContextRepo, workflowMetrics)
	if renderContextSvc == nil {
		return nil, errors.New("iam module: failed to construct render context service")
	}
	tenantAccessRedisHandler, err := iamPubsubHandler.NewTenantAccessRedisHandler(rds, rbacTenantSvc)
	if err != nil {
		return nil, fmt.Errorf("iam module: failed to initialize tenant access Redis handler: %w", err)
	}
	personalAccessRedisHandler, err := iamPubsubHandler.NewPersonalAccessRedisHandler(rds, rbacPlatformSvc)
	if err != nil {
		return nil, fmt.Errorf("iam module: failed to initialize personal access Redis handler: %w", err)
	}

	// [COMMENT]: Khởi tạo các HTTP handlers phục vụ định tuyến API platform/tenant RBAC
	rbacPlatformHandler := iamHandler.NewRbacPlatformHandler(rbacPlatformSvc)
	if rbacPlatformHandler == nil {
		return nil, errors.New("iam module: failed to initialize HTTP RBAC platform handler")
	}

	rbacTenantHandler := iamHandler.NewRbacTenantHandler(rbacTenantSvc)
	if rbacTenantHandler == nil {
		return nil, errors.New("iam module: failed to initialize HTTP RBAC tenant handler")
	}
	renderContextHandler := iamHandler.NewRenderContextHandler(renderContextSvc)
	if renderContextHandler == nil {
		return nil, errors.New("iam module: failed to initialize render context handler")
	}

	// ------------------------------------------------------------------------
	// 🎉 GIAI ĐOẠN 6: RETURN FULLY INITIALIZED MODULE CONTAINER
	// ------------------------------------------------------------------------
	// Trả về container chứa toàn bộ các dependency hoàn toàn hợp lệ, an toàn và sẵn sàng hoạt động.

	return &IAMModule{
		cfg:                                  cfg,
		db:                                   db,
		rds:                                  rds,
		authRedis:                            authRedis,
		L1Registry:                           cacheEngine,
		otel:                                 otel,
		AuthService:                          authSvc,
		lifecycleFactRelay:                   lifecycleFactRelay,
		AuthHandler:                          authHandler,
		UserService:                          userService,
		UserHandler:                          userHandler,
		SelfDeviceHandler:                    selfDeviceHandler,
		PersonalDeviceHandler:                personalDeviceHandler,
		RbacPlatformHandler:                  rbacPlatformHandler,
		RbacTenantHandler:                    rbacTenantHandler,
		RenderContextHandler:                 renderContextHandler,
		MfaHandler:                           mfaHandler,
		RbacPlatformRepository:               rbacPlatformRepo,
		RbacTenantRepository:                 rbacTenantRepo,
		RenderContextRepository:              renderContextRepo,
		SelfDeviceRepository:                 selfDeviceRepo,
		PersonalDeviceRepository:             personalDeviceRepo,
		selfDeviceSvcImpl:                    selfDeviceSvc,
		personalDeviceSvcImpl:                personalDeviceSvc,
		SessionRefreshService:                refreshSvc,
		billingAuthorizationRedisHandler:     billingAuthorizationRedisHandler,
		runtimeReadAuthorizationRedisHandler: runtimeReadAuthorizationRedisHandler,
		authRedisHandler:                     authRedisHandler,
		devicePresenceProjectionHandler:      devicePresenceProjectionHandler,
		deviceSessionCapacityEvictionHandler: deviceSessionCapacityEvictionHandler,
		tenantAccessRedisHandler:             tenantAccessRedisHandler,
		personalAccessRedisHandler:           personalAccessRedisHandler,
	}, nil
}
