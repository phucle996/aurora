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
	iamRpcHandler "controlplane/internal/iam/transport/rpc/handler"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"controlplane/pkg/constant"
	"controlplane/pkg/logger"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type IAMModule struct {
	cfg        *config.Config
	db         *pgxpool.Pool
	rds        *goredis.Client
	L1Registry *cacheengine.CacheRegistry

	// HTTP Transport Handlers (Exposed to the router in API gateway layer)
	AuthHandler   *iamHandler.AuthHandler
	UserHandler   *iamHandler.UserHandler
	DeviceHandler *iamHandler.DeviceHandler
	RbacHandler   *iamHandler.RbacHandler

	// Core Services & Sync Engines
	RbacRepository        iamRepoInterface.RbacRepository
	AuthService           iamSvcInterface.AuthService
	UserService           iamSvcInterface.UserService
	SessionRefreshService iamSvcInterface.SessionRefreshService
	deviceCapCancel       context.CancelFunc
	deviceSvcImpl         iamSvcInterface.DeviceService // giữ interface type để tránh type assertion
}

// NewModule khởi tạo phân hệ IAM. Thiết lập cơ chế Fail-Fast chặt chẽ ở cấp độ biên khởi chạy.
func NewModule(
	cfg *config.Config,
	db *pgxpool.Pool,
	rds *goredis.Client,
	cacheEngine *cacheengine.CacheRegistry,
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

	// Device Repository (PostgreSQL)
	deviceRepo := iamRepoImpl.NewDeviceRepository(cfg, db)
	if deviceRepo == nil {
		return nil, errors.New("iam module: failed to construct device repository")
	}

	// Refresh Token Storage (PostgreSQL)
	refreshTokenRepo := iamRepoImpl.NewRefreshTokenRepository(cfg, db)
	if refreshTokenRepo == nil {
		return nil, errors.New("iam module: failed to construct refresh token repository")
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

	// [COMMENT]: Khởi tạo gRPC connection đến ACR Service phục vụ phân rã & offload Trinity Session
	acrConn, err := grpc.Dial(cfg.ACRGRPCTarget, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, errors.New("iam module: failed to dial ACR gRPC target: " + err.Error())
	}
	acrClient := iamproto.NewSessionServiceClient(acrConn)

	// Device Management Service
	deviceSvc := iamSvcImpl.NewDeviceService(deviceRepo, refreshTokenRepo, cacheEngine, acrClient)
	if deviceSvc == nil {
		return nil, errors.New("iam module: failed to construct user device management service")
	}
	deviceHandler := iamHandler.NewDeviceHandler(deviceSvc)
	if deviceHandler == nil {
		return nil, errors.New("iam module: failed to initialize HTTP device handler")
	}

	// ------------------------------------------------------------------------
	// 🛡️ GIAI ĐOẠN 5: RBAC SYSTEM BOOTSTRAPPING (Di chuyển lên đầu để giải quyết DI)
	// ------------------------------------------------------------------------
	rbacRepo := iamRepoImpl.NewRbacRepository(cfg, db)
	if rbacRepo == nil {
		return nil, errors.New("iam module: failed to construct RBAC repository")
	}

	// Session Refresh Service
	refreshSvc := iamSvcImpl.NewSessionRefreshService(cfg, refreshTokenRepo, rbacRepo, cacheEngine)
	if refreshSvc == nil {
		return nil, errors.New("iam module: failed to construct session refresh service")
	}

	// Khởi tạo repository phục vụ cơ chế Transactional Outbox riêng biệt của module IAM (giải quyết HA & data reliability)
	iamOutboxRepo := iamRepoImpl.NewIamOutboxRepository(db, cfg)
	if iamOutboxRepo == nil {
		return nil, errors.New("iam module: failed to construct IAM outbox repository")
	}

	authSvc := iamSvcImpl.NewAuthService(
		cfg, authRepo, rbacRepo, refreshSvc, deviceSvc,
		cacheEngine, oneTimeTokenSvc, iamOutboxRepo,
		acrClient,
	)
	if authSvc == nil {
		return nil, errors.New("iam module: failed to construct core auth service implementation")
	}

	authHandler := iamHandler.NewAuthHandler(cfg, authSvc)
	if authHandler == nil {
		return nil, errors.New("iam module: failed to initialize HTTP auth handler")
	}

	userService := iamSvcImpl.NewUserService(userRepo, rbacRepo, cacheEngine)
	if userService == nil {
		return nil, errors.New("iam module: failed to construct core user service implementation")
	}

	userHandler := iamHandler.NewUserHandler(userService)
	if userHandler == nil {
		return nil, errors.New("iam module: failed to initialize HTTP user handler")
	}

	// ------------------------------------------------------------------------
	// 🛡️ GIAI ĐOẠN 5: RBAC SYSTEM BOOTSTRAPPING & SYNC
	// ------------------------------------------------------------------------
	// Phân hệ phân quyền truy cập người dùng và đồng bộ hóa cache trên toàn cụm.

	// rbacRepo đã được khởi tạo phía trên phục vụ DI cho refreshSvc

	rbacSvc := iamSvcImpl.NewRbacService(rbacRepo, cacheEngine)
	if rbacSvc == nil {
		return nil, errors.New("iam module: failed to construct RBAC engine service")
	}

	rbacHandler := iamHandler.NewRbacHandler(rbacSvc)
	if rbacHandler == nil {
		return nil, errors.New("iam module: failed to initialize HTTP RBAC handler")
	}

	// ------------------------------------------------------------------------
	// 🎉 GIAI ĐOẠN 6: RETURN FULLY INITIALIZED MODULE CONTAINER
	// ------------------------------------------------------------------------
	// Trả về container chứa toàn bộ các dependency hoàn toàn hợp lệ, an toàn và sẵn sàng hoạt động.

	return &IAMModule{
		cfg:                   cfg,
		db:                    db,
		rds:                   rds,
		L1Registry:            cacheEngine,
		AuthService:           authSvc,
		AuthHandler:           authHandler,
		UserService:           userService,
		UserHandler:           userHandler,
		DeviceHandler:         deviceHandler,
		RbacHandler:           rbacHandler,
		RbacRepository:        rbacRepo,
		deviceSvcImpl:         deviceSvc,
		SessionRefreshService: refreshSvc,
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
	_ = m.deviceSvcImpl.TouchDeviceLastSeen(ctx, deviceUUID)
}

// RegisterGRPCServices đăng ký các dịch vụ gRPC của phân hệ IAM phục vụ xác thực Trinity
func (m *IAMModule) RegisterGRPCServices(server *grpc.Server) {
	if m == nil || m.AuthService == nil || m.SessionRefreshService == nil {
		return
	}
	handler := iamRpcHandler.NewAuthGRPCHandler(m.AuthService, m.SessionRefreshService)
	iamproto.RegisterAuthServiceServer(server, handler)
	logger.SysInfo("grpc", "registered IAM AuthService onto gRPC server")
}
