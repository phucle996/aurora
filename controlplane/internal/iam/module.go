// ============================================================================
// IAM MODULE (CONTROL PLANE STATE ENGINE)
// ============================================================================
//
// 📜 DESIGN CONTRACT (Hợp đồng Thiết kế):
//   1. [Fail-Fast Bootstrapping Contract]: Bất kỳ dependency hệ thống nào bị 'nil' hoặc
//      lỗi kết nối mạng trong pha khởi tạo NewModule() sẽ kích hoạt chặn đứng tiến trình
//      và thoát lập tức (Exit Code > 0). Không bao giờ cho phép Pod chạy ở trạng thái "lỗi ngầm".
//   2. [Interface Binding Contract]: Tất cả dịch vụ nội bộ kết nối thông qua ranh giới
//      Interface được phân cấp tại `domain/repo` và `domain/service`. Tuyệt đối cấm liên kết
//      trực tiếp các implementation cụ thể ở bên ngoài package `iam` để phục vụ Mocking dễ dàng.
//
// 🗄️ SOURCE OF TRUTH - SoT (Nguồn dữ liệu gốc):
//   * [SOT for Dependency Injection & Wiring Graph (Tệp tin module.go)]:
//     - File `module.go` đóng vai trò là SOURCE OF TRUTH duy nhất định nghĩa cách khởi tạo,
//       quản lý vòng đời (Lifecycle), tiêm phụ thuộc (Dependency Injection - DI) thủ công,
//       và thiết lập toàn bộ đồ thị liên kết (Wiring Graph) của tất cả thành phần thuộc phân hệ IAM.
//     - Mọi sự phụ thuộc chéo (cross-dependency) giữa các repository, cache layers, services,
//       và HTTP handlers của phân hệ IAM đều được phản ánh tường minh và chính xác tại đây.
//     - SRE & Tech Lead Ghi chú: File này KHÔNG tự ý quyết định chính sách xử lý lỗi khẩn cấp
//       (như tự gọi panic() hay os.Exit() khi phát hiện dependency bị nil hoặc lỗi khởi tạo).
//       Nó thực hiện kiểm tra kiểm soát toàn vẹn đồ thị DI, phát hiện lỗi và trả lỗi (return error)
//       ngược lên cho Callsite để Callsite chủ động quyết định chính sách Fail (Panic, Exit hay retry/restart pod).
//     - Callsite gọi DI: Được triệu gọi tại `controlplane/internal/app/module.go` (dòng 103).
//
// 🛡️ ARCHITECTURAL BOUNDARY (Ranh giới Thiết kế):
//   - Tầng Transport (HTTP Handlers) <--> Tầng Service (Domain Logic) <--> Tầng Repository (DB Driver).
//   - Tách biệt mô hình dữ liệu: Tầng Service hoàn toàn thao tác trên Domain Entities độc lập.
//     Repository chịu trách nhiệm ánh xạ (Mapper hai chiều) sang Database Models trước khi DB Write.
//   - RAM Cache Invalidation Boundary: Quá trình xoay khóa khẩn cấp ở Pod bất kỳ sẽ phát
//     sự kiện invalidation qua Redis Pub/Sub trên kênh `sre:admin:invalidation`. Ranh giới cache
//     cục bộ trên RAM của tất cả các Node HA còn lại sẽ tự động hội tụ trạng thái mới trong <10ms.
//
// 👥 VAI TRÒ VÀ GHI CHÚ VẬN HÀNH (ROLE-SPECIFIC CHEATSHEET):
//
//   📌 ĐỐI VỚI SRE & DEVOPS PLATFORM ENGINEERS:
//     * Cấu hình Hệ thống:
//       - Yêu cầu kết nối PostgreSQL (`db`) và Redis (`rds`) phải luôn trực tuyến và có cơ chế Auto-Reconnect.
//       - Alerting: Dịch vụ tích hợp Telegram Alerts qua `tgClient` để gửi cảnh báo tức thì
//         khi xảy ra xoay khóa khẩn cấp hoặc brute-force tấn công. Cấu hình bot token tại ENV Config.
//     * Vận hành dọn dẹp định kỳ (Garbage Collection):
//       - Không chạy các Job xóa dữ liệu rác trực tiếp trong tiến trình API Service để tránh chiếm dụng CPU.
//       - Luôn sử dụng CronJob Kubernetes (`k8s/admin-keys-gc-cronjob.yaml`) đã thiết lập sẵn.
//         Lịch chạy mặc định: 00:00 Hàng ngày.
//       - Để chạy GC bằng tay khẩn cấp khi đĩa DB đầy:
//         `kubectl create job --from=cronjob/admin-keys-gc manual-cleanup-job -n controlplane`
//
//   📌 ĐỐI VỚI TECH LEADS:
//     * Quản lý Tài nguyên & DI:
//       - Module này hoạt động như một Container Dependency Injection (DI) thủ công duy nhất của phân hệ.
//       - Nghiêm cấm khởi tạo ad-hoc hoặc import trực tiếp `pgxpool` hay `redis` client vào sâu
//         bên trong các Service layer. Tất cả các tài nguyên bắt buộc phải khai báo qua NewModule.
//       - Khi mở rộng tính năng mới, hãy khai báo Interface tương ứng trước và tích hợp vào DI tại đây.
//
//   📌 ĐỐI VỚI APPLICATION DEVELOPERS:
//     * Quy tắc mở rộng & Sửa đổi mã nguồn:
//       - Luôn đảm bảo không có logic rò rỉ (no leak) giữa Database Models và Domain Entities.
//       - Mọi hàm Service mới phải tuân thủ việc bọc lỗi bằng cấu trúc `apperr.Wrap(...)` kèm theo
//         Taxonomy Error tương ứng để đảm bảo thống nhất chuẩn hóa log định dạng JSON cho ELK/Grafana Loki.
//       - Khi viết hàm Service có thao tác ghi dữ liệu, luôn mở Transaction tại Service Layer và truyền
//         context qua các Repo methods.
// ============================================================================

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
	cfg         *config.Config
	db          *pgxpool.Pool
	rds         *goredis.Client
	L1Registry  *cacheengine.CacheRegistry

	// HTTP Transport Handlers (Exposed to the router in API gateway layer)
	AuthHandler         *iamHandler.AuthHandler
	UserHandler         *iamHandler.UserHandler
	DeviceHandler       *iamHandler.DeviceHandler
	RbacHandler         *iamHandler.RbacHandler

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
