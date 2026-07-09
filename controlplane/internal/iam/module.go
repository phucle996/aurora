package iam

import (
	"context"
	"strings"

	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamRepoImpl "controlplane/internal/iam/repository"
	iamSvcImpl "controlplane/internal/iam/service"
	iamHandler "controlplane/internal/iam/transport/http/handler"
	"controlplane/internal/observability"
	"controlplane/pkg/constant"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	goredis "github.com/redis/go-redis/v9"
)

type IAMModule struct {
	cfg        *config.Config
	db         *pgxpool.Pool
	rds        *goredis.Client
	L1Registry *cacheengine.CacheRegistry
	natsConn   *nats.Conn
	otel       *observability.OTel
	natsSubs   []*nats.Subscription

	// HTTP Transport Handlers (Exposed to the router in API gateway layer)
	AuthHandler           *iamHandler.AuthHandler
	UserHandler           *iamHandler.UserHandler
	DeviceSelfHandler     *iamHandler.DeviceSelfHandler     // [COMMENT]: Handler quản lý thiết bị cá nhân
	DevicePlatformHandler *iamHandler.DevicePlatformHandler // [COMMENT]: Handler giám sát thiết bị platform
	RbacPlatformHandler   *iamHandler.RbacPlatformHandler   // [COMMENT]: Handler cho các tác vụ platform-scoped RBAC
	RbacTenantHandler     *iamHandler.RbacTenantHandler     // [COMMENT]: Handler cho các tác vụ tenant-scoped RBAC

	// Core Services & Sync Engines
	RbacPlatformRepository   iamRepoInterface.RbacPlatformRepository   // [COMMENT]: Repo quản lý platform role
	RbacTenantRepository     iamRepoInterface.RbacTenantRepository     // [COMMENT]: Repo quản lý tenant role
	DeviceSelfRepository     iamRepoInterface.DeviceSelfRepository     // [COMMENT]: Repo quản lý thiết bị cá nhân
	DevicePlatformRepository iamRepoInterface.DevicePlatformRepository // [COMMENT]: Repo quản lý thiết bị platform
	AuthService              iamSvcInterface.AuthService
	UserService              iamSvcInterface.UserService
	SessionRefreshService    iamSvcInterface.SessionRefreshService
	deviceCapCancel          context.CancelFunc
	deviceSelfSvcImpl        iamSvcInterface.DeviceSelfService     // giữ interface type để tránh type assertion
	devicePlatformSvcImpl    iamSvcInterface.DevicePlatformService // giữ interface type để tránh type assertion
}

// NewModule khởi tạo phân hệ IAM. Thiết lập cơ chế Fail-Fast chặt chẽ ở cấp độ biên khởi chạy.
func NewModule(
	cfg *config.Config,
	db *pgxpool.Pool,
	rds *goredis.Client,
	cacheEngine *cacheengine.CacheRegistry,
	natsConn *nats.Conn,
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

	// One Time Token Service
	oneTimeTokenSvc := iamSvcImpl.NewOneTimeTokenService(cfg, cacheEngine)
	if oneTimeTokenSvc == nil {
		return nil, errors.New("iam module: failed to construct one time token service")
	}

	// ------------------------------------------------------------------------
	// 💼 GIAI ĐOẠN 3: SERVICE LAYER INITIALIZATION
	// ------------------------------------------------------------------------
	// Khởi tạo các Engine xử lý Business Logic chính.

	// [COMMENT]: Khởi tạo các Device Service riêng biệt cho Self và Platform (truyền nil acrClient vì chuyển sang Pub/Sub)
	deviceSelfSvc := iamSvcImpl.NewDeviceSelfService(deviceSelfRepo, refreshTokenRepo, cacheEngine, nil)
	if deviceSelfSvc == nil {
		return nil, errors.New("iam module: failed to construct device self service")
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

	// Khởi tạo repository phục vụ cơ chế Transactional Outbox riêng biệt của module IAM (giải quyết HA & data reliability)
	iamOutboxRepo := iamRepoImpl.NewIamOutboxRepository(db, cfg)
	if iamOutboxRepo == nil {
		return nil, errors.New("iam module: failed to construct IAM outbox repository")
	}

	authSvc := iamSvcImpl.NewAuthService(
		cfg, authRepo, refreshSvc, deviceSelfSvc,
		cacheEngine, oneTimeTokenSvc, iamOutboxRepo,
		nil,
	)
	if authSvc == nil {
		return nil, errors.New("iam module: failed to construct core auth service implementation")
	}

	authHandler := iamHandler.NewAuthHandler(cfg, authSvc)
	if authHandler == nil {
		return nil, errors.New("iam module: failed to initialize HTTP auth handler")
	}

	userService := iamSvcImpl.NewUserService(userRepo, cacheEngine)
	if userService == nil {
		return nil, errors.New("iam module: failed to construct core user service implementation")
	}

	userHandler := iamHandler.NewUserHandler(userService)
	if userHandler == nil {
		return nil, errors.New("iam module: failed to initialize HTTP user handler")
	}

	// [COMMENT]: Khởi tạo các service quản lý luồng nghiệp vụ platform/tenant RBAC
	rbacPlatformSvc := iamSvcImpl.NewRbacPlatformService(rbacPlatformRepo, cacheEngine)
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

	// ------------------------------------------------------------------------
	// 🎉 GIAI ĐOẠN 6: RETURN FULLY INITIALIZED MODULE CONTAINER
	// ------------------------------------------------------------------------
	// Trả về container chứa toàn bộ các dependency hoàn toàn hợp lệ, an toàn và sẵn sàng hoạt động.

	return &IAMModule{
		cfg:                      cfg,
		db:                       db,
		rds:                      rds,
		L1Registry:               cacheEngine,
		natsConn:                 natsConn,
		otel:                     otel,
		AuthService:              authSvc,
		AuthHandler:              authHandler,
		UserService:              userService,
		UserHandler:              userHandler,
		DeviceSelfHandler:        deviceSelfHandler,
		DevicePlatformHandler:    devicePlatformHandler,
		RbacPlatformHandler:      rbacPlatformHandler,
		RbacTenantHandler:        rbacTenantHandler,
		RbacPlatformRepository:   rbacPlatformRepo,
		RbacTenantRepository:     rbacTenantRepo,
		DeviceSelfRepository:     deviceSelfRepo,
		DevicePlatformRepository: devicePlatformRepo,
		deviceSelfSvcImpl:        deviceSelfSvc,
		devicePlatformSvcImpl:    devicePlatformSvc,
		SessionRefreshService:    refreshSvc,
	}, nil
}

// TouchDeviceLastSeen triển khai đúng signature của middleware.TouchDeviceLastSeenFn.
// Caller (app layer) truyền trực tiếp iamModule.TouchDeviceLastSeen làm method value — không cần closure.
func (m *IAMModule) TouchDeviceLastSeen(ctx context.Context, trackedDeviceID string, ip *string, userAgent *string) {

	deviceUUID, err := uuid.Parse(strings.TrimSpace(trackedDeviceID))
	if err != nil {
		return
	}
	// [COMMENT]: Đảm bảo tương thích ngược, nếu truyền ip/ua trực tiếp, ta sẽ tiêm chúng vào Context trước khi gọi Service.
	if ip != nil || userAgent != nil {
		var ipStr, uaStr string
		if ip != nil {
			ipStr = *ip
		}
		if userAgent != nil {
			uaStr = *userAgent
		}
		ctx = context.WithValue(ctx, constant.RemoteIPKey, ipStr)
		ctx = context.WithValue(ctx, constant.UserAgentKey, uaStr)
	}
	// Best-effort: lỗi flush không ảnh hưởng flow xác thực.
	_ = m.deviceSelfSvcImpl.TouchDeviceLastSeen(ctx, deviceUUID)
}
