// ======================================================================================================
// 📂 MODULE: controlplane/internal/hierarchy/module.go
//            Đặc Tả Quản Lý Vòng Đời & Inject Dependency Hierarchy Module
// ======================================================================================================
//
// 📜 HIỆP ĐỒNG THIẾT KẾ & BOOTSTRAP AN TOÀN (DESIGN CONTRACT & LIFECYCLE):
//   - Đóng vai trò là trung tâm lắp ghép dependency graph của Hierarchy Module.
//   - Quản lý vòng đời chạy nền (Start/Stop) của các tiến trình background an toàn:
//
//     1) GRACEFUL LIFECYCLE MANAGEMENT:
//        * Đảm bảo mọi background workers (Invalidation Bus, Redis Subscriber)
//          đều được kiểm soát bởi các context cancellation biệt lập.
//        * Tắt gracefully sạch sẽ toàn bộ tài nguyên khi hệ thống shutdown, ngăn chặn rò rỉ RAM/Socket.
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Định hình toàn bộ sơ đồ wiring của Hierarchy Module.
//
// 🔒 RANH GIỚI BẢO MẬT & KIẾN TRÚC (CRITICAL ARCHITECTURAL BOUNDARY):
//   - Ranh giới thiết kế DI (Dependency Injection) khép kín. Không phơi bày cấu trúc persistance thô ra ngoài.
//
// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
//   - Bất kỳ lỗi khởi tạo dependency bắt buộc (DB/Redis) sẽ chặn đứng tiến trình chạy và ném lỗi sớm (Fail-Fast).
//
// ======================================================================================================

package hierarchy

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"controlplane/internal/cacheengine"
	"controlplane/internal/config"

	hierarchyrepo "controlplane/internal/hierarchy/domain/repo"
	hierarchyservice "controlplane/internal/hierarchy/domain/service"
	repository "controlplane/internal/hierarchy/repository"
	service "controlplane/internal/hierarchy/service"
	httphandler "controlplane/internal/hierarchy/transport/http/handler"
	pubsubhandler "controlplane/internal/hierarchy/transport/pubsub/handler"

	"controlplane/internal/observability"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

type Module struct {
	cfg                         *config.Config
	rds                         *goredis.Client
	otel                        *observability.OTel
	zoneRedis                   *pubsubhandler.ZoneRedisHandler
	ZoneRepository              hierarchyrepo.ZoneRepository
	ZoneService                 hierarchyservice.ZoneService
	ZoneHandler                 *httphandler.ZoneHandler
	ZoneEncryptionKeyRepository hierarchyrepo.ZoneEncryptionKeyRepository
	ZoneEncryptionKeyService    hierarchyservice.ZoneEncryptionKeyService
	ZoneEncryptionKeyHandler    *httphandler.ZoneEncryptionKeyHandler
	// [COMMENT]: Chia ranh giới DB access thành 2 repositories Tenant và Personal
	TenantWorkspaceRepository   hierarchyrepo.TenantWorkspaceRepository
	PersonalWorkspaceRepository hierarchyrepo.PersonalWorkspaceRepository
	// [COMMENT]: Chia ranh giới Service Layer thành 2 services Tenant và Personal
	TenantWorkspaceService   hierarchyservice.TenantWorkspaceService
	PersonalWorkspaceService hierarchyservice.PersonalWorkspaceService
	WorkspacePersonalHandler *httphandler.WorkspacePersonalHandler
	WorkspaceTenantHandler   *httphandler.WorkspaceTenantHandler
	TenantRepository         hierarchyrepo.TenantRepository
	TenantService            hierarchyservice.TenantService
	TenantHandler            *httphandler.TenantHandler
	listenCancel             context.CancelFunc
	L1Registry               *cacheengine.CacheRegistry
}

// NewModule dựng dependency graph của Hierarchy và trả về Module hoàn chỉnh.
// Ở đây chúng ta chỉ nhận duy nhất thực thể cacheEngine để truyền vào các service nội bộ.
func NewModule(
	cfg *config.Config,
	db *pgxpool.Pool,
	rds *goredis.Client,
	cacheEngine *cacheengine.CacheRegistry,
	otel *observability.OTel,
) (*Module, error) {
	if cfg == nil {
		return nil, fmt.Errorf("hierarchy module: config is required")
	}
	if db == nil {
		return nil, fmt.Errorf("hierarchy module: database pool is required")
	}
	hierarchySchema := strings.TrimSpace(cfg.SchemaSQL.Hierarchy)
	if !regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`).MatchString(hierarchySchema) {
		return nil, fmt.Errorf("hierarchy module: database schema is invalid")
	}

	// 1) SoT data access for secret lifecycle.
	// 3) Rotation orchestration - Chỉ truyền một đối tượng cacheEngine duy nhất
	zoneRepo := repository.NewZoneRepoImpl(cfg, db)
	if zoneRepo == nil {
		return nil, fmt.Errorf("hierarchy module: zone repository is nil")
	}

	// 5) Zone management service - Chỉ truyền một đối tượng cacheEngine duy nhất
	zoneService := service.NewZoneService(zoneRepo, rds)
	if zoneService == nil {
		return nil, fmt.Errorf("hierarchy module: zone service is nil")
	}
	zHandler := httphandler.NewZoneHandler(zoneService)
	if zHandler == nil {
		return nil, fmt.Errorf("hierarchy module: zone handler is nil")
	}
	zoneRedis, err := pubsubhandler.NewZoneRedisHandler(rds, zoneService, otel)
	if err != nil {
		return nil, fmt.Errorf("hierarchy module: initialize Shared Redis handler: %w", err)
	}

	// [COMMENT]: Hierarchy owns only the public half of each Zone key pair.
	// Dataplane private-key loading remains a separate filesystem boundary.
	zoneEncryptionKeyRepo := repository.NewZoneEncryptionKeyRepository(db, hierarchySchema)
	if zoneEncryptionKeyRepo == nil {
		return nil, fmt.Errorf("hierarchy module: zone encryption key repository is nil")
	}
	zoneEncryptionKeyService := service.NewZoneEncryptionKeyService(zoneEncryptionKeyRepo)
	if zoneEncryptionKeyService == nil {
		return nil, fmt.Errorf("hierarchy module: zone encryption key service is nil")
	}
	zoneEncryptionKeyHTTPHandler := httphandler.NewZoneEncryptionKeyHandler(zoneEncryptionKeyService)
	if zoneEncryptionKeyHTTPHandler == nil {
		return nil, fmt.Errorf("hierarchy module: zone encryption key handler is nil")
	}

	// 6) Workspace management — repo, service, handler (Chia 2 dòng chảy Tenant và Personal)
	tenantWorkspaceRepo := repository.NewTenantWorkspaceRepoImpl(cfg, db)
	if tenantWorkspaceRepo == nil {
		return nil, fmt.Errorf("hierarchy module: tenant workspace repository is nil")
	}
	personalWorkspaceRepo := repository.NewPersonalWorkspaceRepoImpl(cfg, db)
	if personalWorkspaceRepo == nil {
		return nil, fmt.Errorf("hierarchy module: personal workspace repository is nil")
	}

	tenantWorkspaceService := service.NewTenantWorkspaceService(tenantWorkspaceRepo, cacheEngine)
	if tenantWorkspaceService == nil {
		return nil, fmt.Errorf("hierarchy module: tenant workspace service is nil")
	}
	personalWorkspaceService := service.NewPersonalWorkspaceService(personalWorkspaceRepo)
	if personalWorkspaceService == nil {
		return nil, fmt.Errorf("hierarchy module: personal workspace service is nil")
	}
	wPersonalHandler := httphandler.NewWorkspacePersonalHandler(personalWorkspaceService)
	if wPersonalHandler == nil {
		return nil, fmt.Errorf("hierarchy module: workspace personal handler is nil")
	}
	wTenantHandler := httphandler.NewWorkspaceTenantHandler(tenantWorkspaceService)
	if wTenantHandler == nil {
		return nil, fmt.Errorf("hierarchy module: workspace tenant handler is nil")
	}

	// 7) Tenant management — repo, service, handler
	tenantRepo := repository.NewTenantRepoImpl(cfg, db)
	if tenantRepo == nil {
		return nil, fmt.Errorf("hierarchy module: tenant repository is nil")
	}
	tenantService := service.NewTenantService(tenantRepo)
	if tenantService == nil {
		return nil, fmt.Errorf("hierarchy module: tenant service is nil")
	}
	tHandler := httphandler.NewTenantHandler(tenantService)
	if tHandler == nil {
		return nil, fmt.Errorf("hierarchy module: tenant handler is nil")
	}

	// [COMMENT]: Lược bỏ việc khởi tạo BackpressureService do đã chuyển đổi hoàn toàn sang mô hình event-driven ở job-orchestrator.

	return &Module{
		cfg:                         cfg,
		rds:                         rds,
		otel:                        otel,
		zoneRedis:                   zoneRedis,
		ZoneRepository:              zoneRepo,
		ZoneService:                 zoneService,
		ZoneHandler:                 zHandler,
		ZoneEncryptionKeyRepository: zoneEncryptionKeyRepo,
		ZoneEncryptionKeyService:    zoneEncryptionKeyService,
		ZoneEncryptionKeyHandler:    zoneEncryptionKeyHTTPHandler,
		TenantWorkspaceRepository:   tenantWorkspaceRepo,
		PersonalWorkspaceRepository: personalWorkspaceRepo,
		TenantWorkspaceService:      tenantWorkspaceService,
		PersonalWorkspaceService:    personalWorkspaceService,
		WorkspacePersonalHandler:    wPersonalHandler,
		WorkspaceTenantHandler:      wTenantHandler,
		TenantRepository:            tenantRepo,
		TenantService:               tenantService,
		TenantHandler:               tHandler,
		L1Registry:                  cacheEngine,
	}, nil
}

func (m *Module) SetTenantBillingOutboxNotifier(notify func()) error {
	if notify == nil {
		return fmt.Errorf("hierarchy module: tenant billing outbox notifier is nil")
	}
	tenantService, ok := m.TenantService.(*service.TenantService)
	if !ok || tenantService == nil {
		return fmt.Errorf("hierarchy module: concrete tenant service is unavailable")
	}
	tenantService.SetBillingOutboxNotifier(notify)
	return nil
}

// Stop hủy các background goroutine của module Hierarchy an toàn.
func (m *Module) Stop() {
	if m == nil {
		return
	}
	if m.listenCancel != nil {
		m.listenCancel()
		m.listenCancel = nil
	}

	if m.zoneRedis != nil {
		m.zoneRedis.Stop()
	}
}
