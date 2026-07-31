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
	"fmt"
	"regexp"
	"strings"

	"controlplane/internal/cacheengine"
	"controlplane/internal/config"

	hierarchyRepoImpl "controlplane/internal/hierarchy/repository"
	hierarchySvcImpl "controlplane/internal/hierarchy/service"
	hierarchyHandler "controlplane/internal/hierarchy/transport/http/handler"
	hierarchyPubsubHandler "controlplane/internal/hierarchy/transport/pubsub/handler"

	"controlplane/internal/observability"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

type Module struct {
	zoneRedis                *hierarchyPubsubHandler.ZoneRedisHandler
	tenantService            *hierarchySvcImpl.TenantService
	ZoneHandler              *hierarchyHandler.ZoneHandler
	ZoneEncryptionKeyHandler *hierarchyHandler.ZoneEncryptionKeyHandler
	WorkspacePersonalHandler *hierarchyHandler.WorkspacePersonalHandler
	WorkspaceTenantHandler   *hierarchyHandler.WorkspaceTenantHandler
	TenantHandler            *hierarchyHandler.TenantHandler
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
	if rds == nil {
		return nil, fmt.Errorf("hierarchy module: Shared Redis is required")
	}
	if cacheEngine == nil {
		return nil, fmt.Errorf("hierarchy module: cache registry is required")
	}
	if otel == nil {
		return nil, fmt.Errorf("hierarchy module: observability is required")
	}
	metrics := otel.WorkflowRecorder("hierarchy")
	hierarchySchema := strings.TrimSpace(cfg.SchemaSQL.Hierarchy)
	storageSchema := strings.TrimSpace(cfg.SchemaSQL.Storage)
	mailSchema := strings.TrimSpace(cfg.SchemaSQL.Mail)
	hypervisorSchema := strings.TrimSpace(cfg.SchemaSQL.Hypervisor)
	managedServiceSchema := strings.TrimSpace(cfg.SchemaSQL.ManagedService)
	schemaPattern := regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)
	for _, schema := range []string{
		hierarchySchema,
		storageSchema,
		mailSchema,
		hypervisorSchema,
		managedServiceSchema,
	} {
		if !schemaPattern.MatchString(schema) {
			return nil, fmt.Errorf("hierarchy module: database schema is invalid")
		}
	}

	// 1) SoT data access for secret lifecycle.
	// 3) Rotation orchestration - Chỉ truyền một đối tượng cacheEngine duy nhất
	zoneRepo := hierarchyRepoImpl.NewZoneRepoImpl(cfg, db)
	if zoneRepo == nil {
		return nil, fmt.Errorf("hierarchy module: zone repository is nil")
	}

	// 5) Zone management service - Chỉ truyền một đối tượng cacheEngine duy nhất
	zoneService := hierarchySvcImpl.NewZoneService(zoneRepo, rds, metrics)
	if zoneService == nil {
		return nil, fmt.Errorf("hierarchy module: zone service is nil")
	}
	zHandler := hierarchyHandler.NewZoneHandler(zoneService)
	if zHandler == nil {
		return nil, fmt.Errorf("hierarchy module: zone handler is nil")
	}
	zoneRedis, err := hierarchyPubsubHandler.NewZoneRedisHandler(rds, zoneService, otel)
	if err != nil {
		return nil, fmt.Errorf("hierarchy module: initialize Shared Redis handler: %w", err)
	}

	// [COMMENT]: Hierarchy owns only the public half of each Zone key pair.
	// Dataplane private-key loading remains a separate filesystem boundary.
	zoneEncryptionKeyRepo := hierarchyRepoImpl.NewZoneEncryptionKeyRepository(
		db,
		hierarchySchema,
		storageSchema,
		mailSchema,
		hypervisorSchema,
		managedServiceSchema,
	)
	if zoneEncryptionKeyRepo == nil {
		return nil, fmt.Errorf("hierarchy module: zone encryption key repository is nil")
	}
	zoneEncryptionKeyService := hierarchySvcImpl.NewZoneEncryptionKeyService(zoneEncryptionKeyRepo, metrics)
	if zoneEncryptionKeyService == nil {
		return nil, fmt.Errorf("hierarchy module: zone encryption key service is nil")
	}
	zoneEncryptionKeyHTTPHandler := hierarchyHandler.NewZoneEncryptionKeyHandler(zoneEncryptionKeyService)
	if zoneEncryptionKeyHTTPHandler == nil {
		return nil, fmt.Errorf("hierarchy module: zone encryption key handler is nil")
	}

	// 6) Workspace management — repo, service, handler (Chia 2 dòng chảy Tenant và Personal)
	tenantWorkspaceRepo := hierarchyRepoImpl.NewTenantWorkspaceRepoImpl(cfg, db)
	if tenantWorkspaceRepo == nil {
		return nil, fmt.Errorf("hierarchy module: tenant workspace repository is nil")
	}
	personalWorkspaceRepo := hierarchyRepoImpl.NewPersonalWorkspaceRepoImpl(cfg, db)
	if personalWorkspaceRepo == nil {
		return nil, fmt.Errorf("hierarchy module: personal workspace repository is nil")
	}

	tenantWorkspaceService := hierarchySvcImpl.NewTenantWorkspaceService(tenantWorkspaceRepo, cacheEngine, metrics)
	if tenantWorkspaceService == nil {
		return nil, fmt.Errorf("hierarchy module: tenant workspace service is nil")
	}
	personalWorkspaceService := hierarchySvcImpl.NewPersonalWorkspaceService(personalWorkspaceRepo, metrics)
	if personalWorkspaceService == nil {
		return nil, fmt.Errorf("hierarchy module: personal workspace service is nil")
	}
	wPersonalHandler := hierarchyHandler.NewWorkspacePersonalHandler(personalWorkspaceService)
	if wPersonalHandler == nil {
		return nil, fmt.Errorf("hierarchy module: workspace personal handler is nil")
	}
	wTenantHandler := hierarchyHandler.NewWorkspaceTenantHandler(tenantWorkspaceService)
	if wTenantHandler == nil {
		return nil, fmt.Errorf("hierarchy module: workspace tenant handler is nil")
	}

	// 7) Tenant management — repo, service, handler
	tenantRepo := hierarchyRepoImpl.NewTenantRepoImpl(cfg, db)
	if tenantRepo == nil {
		return nil, fmt.Errorf("hierarchy module: tenant repository is nil")
	}
	tenantService := hierarchySvcImpl.NewTenantService(tenantRepo, metrics)
	if tenantService == nil {
		return nil, fmt.Errorf("hierarchy module: tenant service is nil")
	}
	concreteTenantService, ok := tenantService.(*hierarchySvcImpl.TenantService)
	if !ok {
		return nil, fmt.Errorf("hierarchy module: concrete tenant service is unavailable")
	}
	tHandler := hierarchyHandler.NewTenantHandler(tenantService)
	if tHandler == nil {
		return nil, fmt.Errorf("hierarchy module: tenant handler is nil")
	}

	// [COMMENT]: Lược bỏ việc khởi tạo BackpressureService do đã chuyển đổi hoàn toàn sang mô hình event-driven ở job-orchestrator.

	return &Module{
		zoneRedis:                zoneRedis,
		tenantService:            concreteTenantService,
		ZoneHandler:              zHandler,
		ZoneEncryptionKeyHandler: zoneEncryptionKeyHTTPHandler,
		WorkspacePersonalHandler: wPersonalHandler,
		WorkspaceTenantHandler:   wTenantHandler,
		TenantHandler:            tHandler,
		L1Registry:               cacheEngine,
	}, nil
}

func (m *Module) SetTenantBillingOutboxNotifier(notify func()) error {
	if notify == nil {
		return fmt.Errorf("hierarchy module: tenant billing outbox notifier is nil")
	}
	if m.tenantService == nil {
		return fmt.Errorf("hierarchy module: concrete tenant service is unavailable")
	}
	m.tenantService.SetBillingOutboxNotifier(notify)
	return nil
}

// Stop hủy các background goroutine của module Hierarchy an toàn.
func (m *Module) Stop() {
	if m == nil {
		return
	}
	if m.zoneRedis != nil {
		m.zoneRedis.Stop()
	}
}
