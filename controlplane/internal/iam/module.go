package iam

import (
	kafkainfra "controlplane/infra/kafka"
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
	DeviceSelfHandler     *iamHandler.DeviceSelfHandler     // [COMMENT]: Handler quản lý thiết bị cá nhân
	DevicePlatformHandler *iamHandler.DevicePlatformHandler // [COMMENT]: Handler giám sát thiết bị platform
	RbacPlatformHandler   *iamHandler.RbacPlatformHandler   // [COMMENT]: Handler cho các tác vụ platform-scoped RBAC
	RbacTenantHandler     *iamHandler.RbacTenantHandler     // [COMMENT]: Handler cho các tác vụ tenant-scoped RBAC
	MfaHandler            *iamHandler.MfaHandler            // [COMMENT]: Handler phục vụ tra cứu thông tin MFA platform audit

	// Core Services & Sync Engines
	RbacPlatformRepository           iamRepoInterface.RbacPlatformRepository   // [COMMENT]: Repo quản lý platform role
	RbacTenantRepository             iamRepoInterface.RbacTenantRepository     // [COMMENT]: Repo quản lý tenant role
	DeviceSelfRepository             iamRepoInterface.DeviceSelfRepository     // [COMMENT]: Repo quản lý thiết bị cá nhân
	DevicePlatformRepository         iamRepoInterface.DevicePlatformRepository // [COMMENT]: Repo quản lý thiết bị platform
	AuthService                      iamSvcInterface.AuthService
	UserService                      iamSvcInterface.UserService
	SessionRefreshService            iamSvcInterface.SessionRefreshService
	deviceSelfSvcImpl                iamSvcInterface.DeviceSelfService     // giữ interface type để tránh type assertion
	devicePlatformSvcImpl            iamSvcInterface.DevicePlatformService // giữ interface type để tránh type assertion
	billingOutboxRelay               *iamSvcImpl.BillingOutboxRelay
	billingAuthorizationRedisHandler *iamPubsubHandler.BillingAuthorizationRedisHandler
	authRedisHandler                 *iamPubsubHandler.AuthRedisHandler
	deviceRedisHandler               *iamPubsubHandler.DeviceRedisHandler
}

func (m *IAMModule) NotifyBillingOutbox() {
	if m == nil || m.billingOutboxRelay == nil {
		return
	}
	m.billingOutboxRelay.Notify()
}

// NewModule khởi tạo phân hệ IAM. Thiết lập cơ chế Fail-Fast chặt chẽ ở cấp độ biên khởi chạy.
func NewModule(
	cfg *config.Config,
	db *pgxpool.Pool,
	rds *goredis.Client,
	authRedis *goredis.Client,
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
	if kafkaProducer == nil {
		return nil, errors.New("iam module: kafka producer is nil")
	}

	if cacheEngine == nil {
		return nil, errors.New("iam module: cache engine is nil")
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

	// Device Self Repository (PostgreSQL)
	deviceSelfRepo := iamRepoImpl.NewDeviceSelfRepository(cfg, db)
	if deviceSelfRepo == nil {
		return nil, errors.New("iam module: failed to construct device self repository")
	}

	// Device Platform Repository (PostgreSQL)
	devicePlatformRepo := iamRepoImpl.NewDevicePlatformRepository(cfg, db)
	if devicePlatformRepo == nil {
		return nil, errors.New("iam module: failed to construct device platform repository")
	}

	// Refresh Token Storage (PostgreSQL)
	refreshTokenRepo := iamRepoImpl.NewRefreshTokenRepository(cfg, db)
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
	billingAuthorizationRedisHandler, err := iamPubsubHandler.NewBillingAuthorizationRedisHandler(
		rds,
		authRedis,
		rbacPlatformRepo,
		rbacTenantRepo,
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

	deviceSelfSvc := iamSvcImpl.NewDeviceSelfService(deviceSelfRepo, refreshTokenRepo, cacheEngine, rds)
	if deviceSelfSvc == nil {
		return nil, errors.New("iam module: failed to construct device self service")
	}

	deviceRedisHandler, err := iamPubsubHandler.NewDeviceRedisHandler(
		cfg,
		rds,
		deviceSelfSvc,
		otel,
	)
	if err != nil {
		return nil, fmt.Errorf("iam module: failed to initialize Device Redis handler: %w", err)
	}

	devicePlatformSvc := iamSvcImpl.NewDevicePlatformService(devicePlatformRepo)
	if devicePlatformSvc == nil {
		return nil, errors.New("iam module: failed to construct device platform service")
	}

	deviceSelfHandler := iamHandler.NewDeviceSelfHandler(deviceSelfSvc)
	if deviceSelfHandler == nil {
		return nil, errors.New("iam module: failed to initialize HTTP device self handler")
	}
	devicePlatformHandler := iamHandler.NewDevicePlatformHandler(devicePlatformSvc)
	if devicePlatformHandler == nil {
		return nil, errors.New("iam module: failed to initialize HTTP device platform handler")
	}

	// ------------------------------------------------------------------------
	// 🛡️ GIAI ĐOẠN 5: Platform & Tenant RBAC Repos Bootstrapping (giải quyết DI)
	// ------------------------------------------------------------------------

	// [COMMENT]: Khởi tạo Session Refresh Service sử dụng platform và tenant RBAC repos
	refreshSvc := iamSvcImpl.NewSessionRefreshService(cfg, refreshTokenRepo, rbacPlatformRepo, rbacTenantRepo, cacheEngine)
	if refreshSvc == nil {
		return nil, errors.New("iam module: failed to construct session refresh service")
	}

	billingOutboxRepo := iamRepoImpl.NewBillingOutboxRepository(db, cfg)
	billingOutboxRelay, err := iamSvcImpl.NewBillingOutboxRelay(
		billingOutboxRepo,
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

	authSvc := iamSvcImpl.NewAuthService(
		authRepo, refreshSvc, deviceSelfSvc,
		cacheEngine, oneTimeTokenSvc, verificationPublisher,
		billingOutboxRelay,
		nil,
	)
	if authSvc == nil {
		return nil, errors.New("iam module: failed to construct core auth service implementation")
	}

	authRedisHandler, err := iamPubsubHandler.NewAuthRedisHandler(
		cfg,
		rds,
		authSvc,
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

	userService := iamSvcImpl.NewUserService(userRepo, cacheEngine, authRedis, rds)
	if userService == nil {
		return nil, errors.New("iam module: failed to construct core user service implementation")
	}

	userHandler := iamHandler.NewUserHandler(userService)
	if userHandler == nil {
		return nil, errors.New("iam module: failed to initialize HTTP user handler")
	}

	// [COMMENT]: Khởi tạo các service quản lý luồng nghiệp vụ platform/tenant RBAC
	rbacPlatformSvc := iamSvcImpl.NewRbacPlatformService(
		rbacPlatformRepo,
		rbacTenantRepo,
		cacheEngine,
		authRedis,
		rds,
	)
	if rbacPlatformSvc == nil {
		return nil, errors.New("iam module: failed to construct RBAC platform service")
	}

	rbacTenantSvc := iamSvcImpl.NewRbacTenantService(rbacTenantRepo)
	if rbacTenantSvc == nil {
		return nil, errors.New("iam module: failed to construct RBAC tenant service")
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

	// [COMMENT]: Khởi tạo các thành phần DI phục vụ tra cứu trạng thái MFA
	mfaRepo := iamRepoImpl.NewMfaRepository(cfg, db)
	mfaSvc := iamSvcImpl.NewMfaService(mfaRepo)
	mfaHandler := iamHandler.NewMfaHandler(mfaSvc)

	// ------------------------------------------------------------------------
	// 🎉 GIAI ĐOẠN 6: RETURN FULLY INITIALIZED MODULE CONTAINER
	// ------------------------------------------------------------------------
	// Trả về container chứa toàn bộ các dependency hoàn toàn hợp lệ, an toàn và sẵn sàng hoạt động.

	return &IAMModule{
		cfg:                              cfg,
		db:                               db,
		rds:                              rds,
		authRedis:                        authRedis,
		L1Registry:                       cacheEngine,
		otel:                             otel,
		AuthService:                      authSvc,
		billingOutboxRelay:               billingOutboxRelay,
		AuthHandler:                      authHandler,
		UserService:                      userService,
		UserHandler:                      userHandler,
		DeviceSelfHandler:                deviceSelfHandler,
		DevicePlatformHandler:            devicePlatformHandler,
		RbacPlatformHandler:              rbacPlatformHandler,
		RbacTenantHandler:                rbacTenantHandler,
		MfaHandler:                       mfaHandler,
		RbacPlatformRepository:           rbacPlatformRepo,
		RbacTenantRepository:             rbacTenantRepo,
		DeviceSelfRepository:             deviceSelfRepo,
		DevicePlatformRepository:         devicePlatformRepo,
		deviceSelfSvcImpl:                deviceSelfSvc,
		devicePlatformSvcImpl:            devicePlatformSvc,
		SessionRefreshService:            refreshSvc,
		billingAuthorizationRedisHandler: billingAuthorizationRedisHandler,
		authRedisHandler:                 authRedisHandler,
		deviceRedisHandler:               deviceRedisHandler,
	}, nil
}
