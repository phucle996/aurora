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
	infraredis "controlplane/infra/redis"
	"controlplane/infra/telegram"
	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	iamCache "controlplane/internal/iam/cache"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	coreSvc "controlplane/internal/iam/domain/service"
	iamRepoImpl "controlplane/internal/iam/repository"
	iamSvcImpl "controlplane/internal/iam/service"
	iamHandler "controlplane/internal/iam/transport/http/handler"
	"controlplane/internal/security/ratelimit"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

type IAMModule struct {
	cfg         *config.Config
	db          *pgxpool.Pool
	rds         *goredis.Client
	rateLimiter *ratelimit.Bucket
	L1Registry  *cacheengine.CacheRegistry

	// HTTP Transport Handlers (Exposed to the router in API gateway layer)
	AuthHandler         *iamHandler.AuthHandler
	LogoutHandler       *iamHandler.LogoutHandler
	RefreshTokenHandler *iamHandler.RefreshTokenHandler
	DeviceHandler       *iamHandler.DeviceHandler
	AdminAuthHandler    *iamHandler.AdminAuthHandler
	RbacHandler         *iamHandler.RbacHandler

	// Core Services & Sync Engines
	RbacRepository        iamRepoInterface.RbacRepository
	AdminAPIKeyRepository iamRepoInterface.AdminAPIKeyRepository
	AdminAPIKeyService    coreSvc.AdminAPIKeyService
	userDeviceRuntime  iamCache.UserDeviceRuntimeCache
	rotationCancel     context.CancelFunc
	finalizeCancel     context.CancelFunc
	deviceCapCancel    context.CancelFunc
	authSvcImpl        *iamSvcImpl.AuthService
}

// NewModule khởi tạo phân hệ IAM. Thiết lập cơ chế Fail-Fast chặt chẽ ở cấp độ biên khởi chạy.
func NewModule(
	cfg *config.Config,
	db *pgxpool.Pool,
	rds *goredis.Client,
	rdsJob *goredis.Client,
	rateLimiter *ratelimit.Bucket,
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
	if rdsJob == nil {
		return nil, errors.New("iam module: redis job client (rdsJob) is nil (check redis sentinel/cluster endpoint)")
	}
	if rateLimiter == nil {
		return nil, errors.New("iam module: global rate limiter bucket (rateLimiter) is nil")
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

	// Device Repository (PostgreSQL)
	deviceRepo := iamRepoImpl.NewDeviceRepository(cfg, db)
	if deviceRepo == nil {
		return nil, errors.New("iam module: failed to construct device repository")
	}

	// User Device Runtime (Redis Cache) - Kiểm soát phiên đăng nhập phiên thiết bị phân tán
	userDeviceRuntime := iamCache.NewUserDeviceRuntimeCache(rds)
	if userDeviceRuntime == nil {
		return nil, errors.New("iam module: failed to initialize user device runtime cache (redis client is offline)")
	}

	// Refresh Token Storage (PostgreSQL)
	refreshTokenRepo := iamRepoImpl.NewRefreshTokenRepository(cfg, db)
	if refreshTokenRepo == nil {
		return nil, errors.New("iam module: failed to construct refresh token repository")
	}

	// One Time Token Cache & Service (Redis-backed OTP/Password-less verification)
	otTokenCache := iamCache.NewOneTimeTokenCache(rds)
	if otTokenCache == nil {
		return nil, errors.New("iam module: failed to initialize one time token cache")
	}
	oneTimeTokenSvc := iamSvcImpl.NewOneTimeTokenService(cfg, otTokenCache)
	if oneTimeTokenSvc == nil {
		return nil, errors.New("iam module: failed to construct one time token service")
	}

	// Redis Stream Event Publisher - Phát sự kiện bảo mật (Security audit logging)
	streamPublisher := infraredis.NewRedisStreamPublisher(rdsJob)
	if streamPublisher == nil {
		return nil, errors.New("iam module: failed to initialize redis stream event publisher")
	}

	// Distributed Locks (Chống brute-force và kiểm soát dung lượng thiết bị tối đa)
	capLock := iamCache.NewUserDeviceCapLock(rds)
	if capLock == nil {
		return nil, errors.New("iam module: failed to initialize user device capacity distributed lock")
	}

	// Register Presence Cache (Chống Race Condition khi đăng ký tài khoản trùng lặp)
	regPresenceCache := iamCache.NewRegisterPresenceCache(rds)
	if regPresenceCache == nil {
		return nil, errors.New("iam module: failed to initialize registration presence cache")
	}

	// ------------------------------------------------------------------------
	// 💼 GIAI ĐOẠN 3: SERVICE LAYER INITIALIZATION
	// ------------------------------------------------------------------------
	// Khởi tạo các Engine xử lý Business Logic chính.

	authSvcImpl := iamSvcImpl.NewAuthServiceImpl(
		cfg, authRepo, refreshTokenRepo, deviceRepo,
		cacheEngine, oneTimeTokenSvc, streamPublisher,
	)
	if authSvcImpl == nil {
		return nil, errors.New("iam module: failed to construct core auth service implementation")
	}

	authSvc := iamSvcImpl.WrapAuthService(authSvcImpl)
	if authSvc == nil {
		return nil, errors.New("iam module: failed to wrap auth service")
	}

	authHandler := iamHandler.NewAuthHandler(cfg, authSvc)
	if authHandler == nil {
		return nil, errors.New("iam module: failed to initialize HTTP auth handler")
	}

	logoutHandler := iamHandler.NewLogoutHandler(cfg, authSvc)
	if logoutHandler == nil {
		return nil, errors.New("iam module: failed to initialize HTTP logout handler")
	}

	// Refresh Token Service
	refreshTokenSvc := iamSvcImpl.NewRefreshTokenService(cfg, refreshTokenRepo, userDeviceRuntime, cacheEngine)
	if refreshTokenSvc == nil {
		return nil, errors.New("iam module: failed to construct refresh token service")
	}
	refreshTokenHandler := iamHandler.NewRefreshTokenHandler(cfg, refreshTokenSvc)
	if refreshTokenHandler == nil {
		return nil, errors.New("iam module: failed to initialize HTTP refresh token handler")
	}

	// Device Management Service
	deviceSvc := iamSvcImpl.NewDeviceService(deviceRepo, refreshTokenRepo, userDeviceRuntime, streamPublisher)
	if deviceSvc == nil {
		return nil, errors.New("iam module: failed to construct user device management service")
	}
	deviceHandler := iamHandler.NewDeviceHandler(deviceSvc)
	if deviceHandler == nil {
		return nil, errors.New("iam module: failed to initialize HTTP device handler")
	}

	// ------------------------------------------------------------------------
	// 🔐 GIAI ĐOẠN 4: SRE ADMIN SECURITY FLOW (HIGH RESILIENCE SRE CORNER)
	// ------------------------------------------------------------------------
	// Khởi tạo quy trình xoay vòng khóa khẩn cấp SRE và cấu hình đồng bộ hóa Cache.

	adminRepo := iamRepoImpl.NewAdminAPIKeyRepository(cfg, db)
	if adminRepo == nil {
		return nil, errors.New("iam module: failed to construct SRE admin key repository")
	}

	// Telegram Alert Channel (SRE Incident Response Alerting)
	tgClient := telegram.NewTelegramClient(cfg.Telegram.BotToken, cfg.Telegram.ChatID)
	if tgClient == nil {
		return nil, errors.New("iam module: failed to establish Telegram client connection")
	}

	// SRE Admin API Key Service (Nơi điều khiển xoay khóa khẩn cấp và Pub/Sub Invalidation)
	adminSvc := iamSvcImpl.NewAdminAPIKeyService(
		cfg, adminRepo, tgClient, cacheEngine,
	)
	if adminSvc == nil {
		return nil, errors.New("iam module: failed to construct SRE admin API key management service")
	}

	adminAuthHandler := iamHandler.NewAdminAuthHandler(cfg, adminSvc)
	if adminAuthHandler == nil {
		return nil, errors.New("iam module: failed to initialize HTTP admin authentication handler")
	}

	// ------------------------------------------------------------------------
	// 🛡️ GIAI ĐOẠN 5: RBAC SYSTEM BOOTSTRAPPING & SYNC
	// ------------------------------------------------------------------------
	// Phân hệ phân quyền truy cập người dùng và đồng bộ hóa cache trên toàn cụm.

	rbacRepo := iamRepoImpl.NewRbacRepository(cfg, db)
	if rbacRepo == nil {
		return nil, errors.New("iam module: failed to construct RBAC repository")
	}

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
		cfg:                 cfg,
		db:                  db,
		rds:                 rds,
		rateLimiter:         rateLimiter,
		L1Registry:          cacheEngine,
		AuthHandler:         authHandler,
		authSvcImpl:         authSvcImpl,
		LogoutHandler:       logoutHandler,
		RefreshTokenHandler: refreshTokenHandler,
		DeviceHandler:       deviceHandler,
		AdminAuthHandler:    adminAuthHandler,
		RbacHandler:         rbacHandler,
		RbacRepository:        rbacRepo,
		AdminAPIKeyRepository: adminRepo,
		AdminAPIKeyService:    adminSvc,
		userDeviceRuntime:     userDeviceRuntime,
	}, nil
}
