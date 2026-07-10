// ======================================================================================================
// 📂 MODULE: controlplane/internal/hierarchy/module.go
//            Đặc Tả Quản Lý Vòng Đời & Inject Dependency Core Module
// ======================================================================================================
//
// 📜 HIỆP ĐỒNG THIẾT KẾ & BOOTSTRAP AN TOÀN (DESIGN CONTRACT & LIFECYCLE):
//   - Đóng vai trò là trung tâm lắp ghép đồ thị phụ thuộc (Dependency Graph Builder) của Core Module.
//   - Quản lý vòng đời chạy nền (Start/Stop) của các tiến trình background an toàn:
//
//     1) GRACEFUL LIFECYCLE MANAGEMENT:
//        * Đảm bảo mọi background workers (Invalidation Bus, Redis Subscriber)
//          đều được kiểm soát bởi các context cancellation biệt lập.
//        * Tắt gracefully sạch sẽ toàn bộ tài nguyên khi hệ thống shutdown, ngăn chặn rò rỉ RAM/Socket.
//
//     2) PORTABLE gRPC REGISTRATION PORT:
//        * Cung cấp phương thức `RegisterGRPCServices` cho tầng app bootstrap đăng ký các API RPC của Core.
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Định hình toàn bộ sơ đồ wiring của Core Module.
//
// 🔒 RANH GIỚI BẢO MẬT & KIẾN TRÚC (CRITICAL ARCHITECTURAL BOUNDARY):
//   - Ranh giới thiết kế DI (Dependency Injection) khép kín. Không phơi bày cấu trúc persistance thô ra ngoài.
//
// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
//   - Bất kỳ lỗi khởi tạo dependency bắt buộc (DB/Redis) sẽ chặn đứng tiến trình chạy và ném lỗi sớm (Fail-Fast).
//
// ======================================================================================================

package core

import (
	"context"
	"fmt"

	"controlplane/internal/cacheengine"
	"controlplane/internal/config"

	coreRepoInterface "controlplane/internal/hierarchy/domain/repo"
	coreSvcInterface "controlplane/internal/hierarchy/domain/service"
	coreRepoImpl "controlplane/internal/hierarchy/repository"
	coreSvcImpl "controlplane/internal/hierarchy/service"
	zoneHandler "controlplane/internal/hierarchy/transport/http/handler"

	"controlplane/internal/observability"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	goredis "github.com/redis/go-redis/v9"
)

type Module struct {
	cfg            *config.Config
	rds            *goredis.Client
	natsConn       *nats.Conn
	otel           *observability.OTel
	natsSubs       []*nats.Subscription
	ZoneRepository coreRepoInterface.ZoneRepository
	ZoneService    coreSvcInterface.ZoneService
	ZoneHandler    *zoneHandler.ZoneHandler
	// [COMMENT]: Chia ranh giới DB access thành 2 repositories Tenant và Personal
	TenantWorkspaceRepository   coreRepoInterface.TenantWorkspaceRepository
	PersonalWorkspaceRepository coreRepoInterface.PersonalWorkspaceRepository
	// [COMMENT]: Chia ranh giới Service Layer thành 2 services Tenant và Personal
	TenantWorkspaceService   coreSvcInterface.TenantWorkspaceService
	PersonalWorkspaceService coreSvcInterface.PersonalWorkspaceService
	WorkspacePersonalHandler *zoneHandler.WorkspacePersonalHandler
	WorkspaceTenantHandler   *zoneHandler.WorkspaceTenantHandler
	TenantRepository         coreRepoInterface.TenantRepository
	TenantService            coreSvcInterface.TenantService
	TenantHandler            *zoneHandler.TenantHandler
	listenCancel             context.CancelFunc
	L1Registry               *cacheengine.CacheRegistry
}

// NewModule dựng dependency graph của Core và trả về Module hoàn chỉnh.
// Ở đây chúng ta chỉ nhận duy nhất thực thể cacheEngine để truyền vào các service nội bộ.
func NewModule(
	cfg *config.Config,
	db *pgxpool.Pool,
	rds *goredis.Client,
	cacheEngine *cacheengine.CacheRegistry,
	natsConn *nats.Conn,
	otel *observability.OTel,
) (*Module, error) {
	if cfg == nil {
		return nil, fmt.Errorf("zone module: config is required")
	}
	if db == nil {
		return nil, fmt.Errorf("zone module: database pool is required")
	}

	// 1) SoT data access for secret lifecycle.
	// 3) Rotation orchestration - Chỉ truyền một đối tượng cacheEngine duy nhất
	zoneRepo := coreRepoImpl.NewZoneRepoImpl(cfg, db)
	if zoneRepo == nil {
		return nil, fmt.Errorf("zone module: zone service unavailable: zone repository is nil")
	}

	// 5) Zone management service - Chỉ truyền một đối tượng cacheEngine duy nhất
	zoneService := coreSvcImpl.NewZoneService(zoneRepo, rds, natsConn)
	if zoneService == nil {
		return nil, fmt.Errorf("zone module: zone service unavailable: zone service is nil")
	}
	zHandler := zoneHandler.NewZoneHandler(zoneService)
	if zHandler == nil {
		return nil, fmt.Errorf("zone module: zone handler is nil")
	}

	// 6) Workspace management — repo, service, handler (Chia 2 dòng chảy Tenant và Personal)
	tenantWorkspaceRepo := coreRepoImpl.NewTenantWorkspaceRepoImpl(cfg, db)
	if tenantWorkspaceRepo == nil {
		return nil, fmt.Errorf("hierarchy module: tenant workspace repository is nil")
	}
	personalWorkspaceRepo := coreRepoImpl.NewPersonalWorkspaceRepoImpl(cfg, db)
	if personalWorkspaceRepo == nil {
		return nil, fmt.Errorf("hierarchy module: personal workspace repository is nil")
	}

	tenantWorkspaceService := coreSvcImpl.NewTenantWorkspaceService(tenantWorkspaceRepo, cacheEngine)
	if tenantWorkspaceService == nil {
		return nil, fmt.Errorf("hierarchy module: tenant workspace service is nil")
	}
	personalWorkspaceService := coreSvcImpl.NewPersonalWorkspaceService(personalWorkspaceRepo)
	if personalWorkspaceService == nil {
		return nil, fmt.Errorf("hierarchy module: personal workspace service is nil")
	}
	wPersonalHandler := zoneHandler.NewWorkspacePersonalHandler(personalWorkspaceService)
	if wPersonalHandler == nil {
		return nil, fmt.Errorf("hierarchy module: workspace personal handler is nil")
	}
	wTenantHandler := zoneHandler.NewWorkspaceTenantHandler(tenantWorkspaceService)
	if wTenantHandler == nil {
		return nil, fmt.Errorf("hierarchy module: workspace tenant handler is nil")
	}

	// 7) Tenant management — repo, service, handler
	tenantRepo := coreRepoImpl.NewTenantRepoImpl(cfg, db)
	if tenantRepo == nil {
		return nil, fmt.Errorf("hierarchy module: tenant repository is nil")
	}
	tenantService := coreSvcImpl.NewTenantService(tenantRepo)
	if tenantService == nil {
		return nil, fmt.Errorf("hierarchy module: tenant service is nil")
	}
	tHandler := zoneHandler.NewTenantHandler(tenantService)
	if tHandler == nil {
		return nil, fmt.Errorf("hierarchy module: tenant handler is nil")
	}

	// [COMMENT]: Lược bỏ việc khởi tạo BackpressureService do đã chuyển đổi hoàn toàn sang mô hình event-driven ở job-orchestrator.

	return &Module{
		cfg:                         cfg,
		rds:                         rds,
		natsConn:                    natsConn,
		otel:                        otel,
		ZoneRepository:              zoneRepo,
		ZoneService:                 zoneService,
		ZoneHandler:                 zHandler,
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

// Stop hủy các background goroutine của module Core an toàn.
func (m *Module) Stop() {
	if m == nil {
		return
	}
	if m.listenCancel != nil {
		m.listenCancel()
		m.listenCancel = nil
	}

	// [COMMENT]: Hủy đăng ký NATS subscriptions trước khi tắt ứng dụng
	for _, sub := range m.natsSubs {
		if sub != nil {
			_ = sub.Unsubscribe()
		}
	}
	m.natsSubs = nil
}
