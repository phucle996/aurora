// ============================================================================
// MAIL MODULE (CONTROL PLANE STATE ENGINE)
// ============================================================================
//
// 📜 DESIGN CONTRACT (Hợp đồng Thiết kế):
//   1. [Fail-Fast Bootstrapping Contract]: Bất kỳ dependency hệ thống nào bị 'nil' hoặc
//      lỗi kết nối mạng trong pha khởi tạo NewModule() sẽ kích hoạt chặn đứng tiến trình
//      và thoát lập tức (Exit Code > 0). Không bao giờ cho phép Pod chạy ở trạng thái "lỗi ngầm".
//   2. [Interface Binding Contract]: Tất cả dịch vụ nội bộ kết nối thông qua ranh giới
//      Interface được phân cấp tại `domain/repo` và `domain/service`. Tuyệt đối cấm liên kết
//      trực tiếp các implementation cụ thể ở bên ngoài package `mail` để phục vụ Mocking dễ dàng.
//
// 🗄️ SOURCE OF TRUTH - SoT (Nguồn dữ liệu gốc):
//   * [SOT for Dependency Injection & Wiring Graph (Tệp tin module.go)]:
//     - File `module.go` đóng vai trò là SOURCE OF TRUTH duy nhất định nghĩa cách khởi tạo,
//       quản lý vòng đời (Lifecycle), tiêm phụ thuộc (Dependency Injection - DI) thủ công,
//       và thiết lập toàn bộ đồ thị liên kết (Wiring Graph) của tất cả thành phần thuộc phân hệ Mail.
//     - Mọi sự phụ thuộc chéo (cross-dependency) giữa các repository, cache layers, services,
//       và HTTP handlers của phân hệ Mail đều được phản ánh tường minh và chính xác tại đây.
//     - SRE & Tech Lead Ghi chú: File này KHÔNG tự ý quyết định chính sách xử lý lỗi khẩn cấp
//       (như tự gọi panic() hay os.Exit() khi phát hiện dependency bị nil hoặc lỗi khởi tạo).
//       Nó thực hiện kiểm tra kiểm soát toàn vẹn đồ thị DI, phát hiện lỗi và trả lỗi (return error)
//       ngược lên cho Callsite để Callsite chủ động quyết định chính sách Fail (Panic, Exit hay retry/restart pod).
//
// 🛡️ ARCHITECTURAL BOUNDARY (Ranh giới Thiết kế):
//   - Tầng Transport (HTTP Handlers) <--> Tầng Service (Domain Logic) <--> Tầng Repository (DB Driver).
//   - Tách biệt mô hình dữ liệu: Tầng Service hoàn toàn thao tác trên Domain Entities độc lập.
//     Repository chịu trách nhiệm ánh xạ (Mapper hai chiều) sang Database Models trước khi DB Write.
//
// 👥 VAI TRÒ VÀ GHI CHÚ VẬN HÀNH (ROLE-SPECIFIC CHEATSHEET):
//
//   📌 ĐỐI VỚI SRE & DEVOPS PLATFORM ENGINEERS:
//     * Cấu hình Hệ thống:
//       - Yêu cầu kết nối PostgreSQL (`db`) và Redis (`rds`) phải luôn trực tuyến và có cơ chế Auto-Reconnect.
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
// ============================================================================

package mail

import (
	"context"
	"errors"

	"controlplane/internal/config"
	"controlplane/internal/core"
	mailCache "controlplane/internal/mail/cache"
	mailRepoInterface "controlplane/internal/mail/domain/repo"
	mailSvcInterface "controlplane/internal/mail/domain/service"
	mailRepoImpl "controlplane/internal/mail/repository/postgres"
	mailSvcImpl "controlplane/internal/mail/service"
	mailHandler "controlplane/internal/mail/transport/http/handler"
	"controlplane/internal/security/ratelimit"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

type Module struct {
	enabled bool
	err     error // Stores initialization error for observability & diagnostics
	cfg     *config.Config

	// 1) Repositories
	ConsumerRepo mailRepoInterface.ConsumerRepository
	TemplateRepo mailRepoInterface.TemplateRepository
	GatewayRepo  mailRepoInterface.GatewayRepository
	EndpointRepo mailRepoInterface.EndpointRepository
	OutboxRepo   mailRepoInterface.MailOutboxRepository

	// 2) Services
	ConsumerService mailSvcInterface.ConsumerService
	TemplateService mailSvcInterface.TemplateService
	GatewayService  mailSvcInterface.GatewayService
	EndpointService mailSvcInterface.EndpointService
	OutboxPoller    *mailSvcImpl.MailOutboxPoller

	// 3) Handlers
	ConsumerHandler *mailHandler.ConsumerHandler
	TemplateHandler *mailHandler.TemplateHandler
	GatewayHandler  *mailHandler.GatewayHandler
	EndpointHandler *mailHandler.EndpointHandler

	// 4) Cache & Queue
	MailCache    *mailCache.MailCache
	JobPublisher *mailCache.JobPublisher

	// 5) Security
	RateLimiter *ratelimit.Bucket

	// 6) Lifecycle Control
	pollerCancel context.CancelFunc
}

// IsEnabled returns true if the module was successfully initialized and is ready to serve.
func (m *Module) IsEnabled() bool {
	return m != nil && m.enabled
}

// GetError returns the initialization error if any, supporting telemetry and SRE auditing.
func (m *Module) GetError() error {
	if m == nil {
		return errors.New("mail module is nil")
	}
	return m.err
}

// NewDegradedModule constructs a muted/degraded Mail module instance carrying the failure context.
// Prevents downstream nil-pointer crashes and permits selective graceful degradation.
func NewDegradedModule(err error) *Module {
	return &Module{
		enabled: false,
		err:     err,
	}
}

// NewModule constructs the Dependency Graph for the Mail Module.
// coreModule is required to resolve cross-module dependencies (e.g. ZoneService for endpoint zone resolution).
func NewModule(cfg *config.Config, db *pgxpool.Pool, rdsCore *goredis.Client, rdsJob *goredis.Client, rateLimiter *ratelimit.Bucket, coreModule *core.Module) (*Module, error) {
	if coreModule == nil {
		return nil, errors.New("mail module: core module is required for cross-module zone resolution")
	}

	// ------------------------------------------------------------------------
	// 🔄 GIAI ĐOẠN 1: CORE REPOSITORIES & CACHES BOOTSTRAPPING
	// ------------------------------------------------------------------------

	// Initialize Repositories
	consumerRepo := mailRepoImpl.NewConsumerRepository(db, cfg)
	if consumerRepo == nil {
		return nil, errors.New("mail module: failed to construct consumer repository")
	}
	templateRepo := mailRepoImpl.NewTemplateRepository(db, cfg)
	if templateRepo == nil {
		return nil, errors.New("mail module: failed to construct template repository")
	}
	gatewayRepo := mailRepoImpl.NewGatewayRepository(db, cfg)
	if gatewayRepo == nil {
		return nil, errors.New("mail module: failed to construct gateway repository")
	}
	endpointRepo := mailRepoImpl.NewEndpointRepository(db, cfg)
	if endpointRepo == nil {
		return nil, errors.New("mail module: failed to construct endpoint repository")
	}
	outboxRepo := mailRepoImpl.NewMailOutboxRepository(db, cfg)
	if outboxRepo == nil {
		return nil, errors.New("mail module: failed to construct outbox repository")
	}

	// Initialize Cache & Publisher
	mCache := mailCache.NewMailCache(rdsCore, cfg)
	if mCache == nil {
		return nil, errors.New("mail module: failed to initialize mail cache")
	}
	publisher := mailCache.NewJobPublisher(rdsJob, cfg)
	if publisher == nil {
		return nil, errors.New("mail module: failed to initialize job publisher")
	}

	// ------------------------------------------------------------------------
	// 💼 GIAI ĐOẠN 2: SERVICE LAYER INITIALIZATION
	// ------------------------------------------------------------------------

	// Initialize Services
	consumerSvc := mailSvcImpl.NewConsumerService(cfg, consumerRepo)
	if consumerSvc == nil {
		return nil, errors.New("mail module: failed to construct consumer service")
	}
	templateSvc := mailSvcImpl.NewTemplateService(cfg, templateRepo)
	if templateSvc == nil {
		return nil, errors.New("mail module: failed to construct template service")
	}
	gatewaySvc := mailSvcImpl.NewGatewayService(cfg, gatewayRepo)
	if gatewaySvc == nil {
		return nil, errors.New("mail module: failed to construct gateway service")
	}
	endpointSvc := mailSvcImpl.NewEndpointService(cfg, endpointRepo, outboxRepo, rdsJob, coreModule.L1Registry)
	if endpointSvc == nil {
		return nil, errors.New("mail module: failed to construct endpoint service")
	}
	outboxPoller := mailSvcImpl.NewMailOutboxPoller(cfg, outboxRepo, rdsJob)
	if outboxPoller == nil {
		return nil, errors.New("mail module: failed to construct outbox poller")
	}

	// ------------------------------------------------------------------------
	// 🎛️ GIAI ĐOẠN 3: TRANSPORT LAYER (HTTP HANDLERS) INITIALIZATION
	// ------------------------------------------------------------------------

	// Initialize Handlers
	consumerHandler := mailHandler.NewConsumerHandler(consumerSvc)
	if consumerHandler == nil {
		return nil, errors.New("mail module: failed to construct consumer HTTP handler")
	}
	templateHandler := mailHandler.NewTemplateHandler(templateSvc)
	if templateHandler == nil {
		return nil, errors.New("mail module: failed to construct template HTTP handler")
	}
	gatewayHandler := mailHandler.NewGatewayHandler(gatewaySvc)
	if gatewayHandler == nil {
		return nil, errors.New("mail module: failed to construct gateway HTTP handler")
	}
	endpointHandler := mailHandler.NewEndpointHandler(endpointSvc)
	if endpointHandler == nil {
		return nil, errors.New("mail module: failed to construct endpoint HTTP handler")
	}

	return &Module{
		enabled:         true,
		cfg:             cfg,
		ConsumerRepo:    consumerRepo,
		TemplateRepo:    templateRepo,
		GatewayRepo:     gatewayRepo,
		EndpointRepo:    endpointRepo,
		OutboxRepo:      outboxRepo,
		ConsumerService: consumerSvc,
		TemplateService: templateSvc,
		GatewayService:  gatewaySvc,
		EndpointService: endpointSvc,
		OutboxPoller:    outboxPoller,
		ConsumerHandler: consumerHandler,
		TemplateHandler: templateHandler,
		GatewayHandler:  gatewayHandler,
		EndpointHandler: endpointHandler,
		MailCache:       mCache,
		JobPublisher:    publisher,
		RateLimiter:     rateLimiter,
	}, nil
}

// Start manages any background process lifecycles (draining, heartbeats if any).
func (m *Module) Start(ctx context.Context) error {
	if !m.IsEnabled() {
		return nil
	}
	if m.OutboxPoller != nil && m.pollerCancel == nil {
		pollerCtx, cancel := context.WithCancel(ctx)
		m.pollerCancel = cancel
		go m.OutboxPoller.Start(pollerCtx)
	}
	return nil
}

// Stop cleanly terminates background tasks during graceful control plane shutdown.
func (m *Module) Stop(ctx context.Context) error {
	if !m.IsEnabled() {
		return nil
	}
	if m.pollerCancel != nil {
		m.pollerCancel()
		m.pollerCancel = nil
	}
	return nil
}
