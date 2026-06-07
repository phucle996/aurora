// ======================================================================================================
// 📂 MODULE: controlplane/internal/core/module.go
//            Đặc Tả Quản Lý Vòng Đời & Inject Dependency Core Module
// ======================================================================================================
//
// 📜 HIỆP ĐỒNG THIẾT KẾ & BOOTSTRAP AN TOÀN (DESIGN CONTRACT & LIFECYCLE):
//   - Đóng vai trò là trung tâm lắp ghép đồ thị phụ thuộc (Dependency Graph Builder) của Core Module.
//   - Quản lý vòng đời chạy nền (Start/Stop) của các tiến trình background an toàn:
//
//     1) GRACEFUL LIFECYCLE MANAGEMENT:
//        * Đảm bảo mọi background workers (Invalidation Bus, Dataplane Orchestrator, Redis Subscriber)
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
	coreCache "controlplane/internal/core/cache"
	coreEntity "controlplane/internal/core/domain/entity"
	coreRepoInterface "controlplane/internal/core/domain/repo"
	coreSvcInterface "controlplane/internal/core/domain/service"
	coreRepoImpl "controlplane/internal/core/repository"
	coreSvcImpl "controlplane/internal/core/service"
	coreHandler "controlplane/internal/core/transport/http/handler"
	coreRpcHandler "controlplane/internal/core/transport/rpc/handler"
	coreProto "controlplane/internal/core/transport/rpc/proto"
	"controlplane/internal/security/ratelimit"
	"controlplane/pkg/logger"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
)

type Module struct {
	cfg                          *config.Config
	SecretRepository             coreRepoInterface.SecretRepository
	SecretRotationService        coreSvcInterface.SecretRotationService
	SecretReadService            coreSvcInterface.SecretReadService
	RuntimeSecretProvider        coreSvcInterface.RuntimeSecretProvider
	ZoneRepository               coreRepoInterface.ZoneRepository
	ZoneService                  coreSvcInterface.ZoneService
	ZoneHandler                  *coreHandler.ZoneHandler
	DataplaneNodeRepository      coreRepoInterface.DataplaneNodeRepository
	DataplaneNodeService         coreSvcInterface.DataplaneNodeService
	DataplaneOrchestrator        *coreSvcImpl.DataplaneOrchestrator
	DataplaneHeartbeatSubscriber *coreSvcImpl.DataplaneHeartbeatSubscriber
	invalidationBus              *coreCache.RedisSecretInvalidationBus
	listenCancel                 context.CancelFunc
	orchestratorCancel           context.CancelFunc
	subscriberCancel             context.CancelFunc
	rateLimiter                  *ratelimit.Bucket
	L1Registry                   *cacheengine.CacheRegistry
}

// NewModule dựng dependency graph của Core và trả về Module hoàn chỉnh.
func NewModule(
	cfg *config.Config,
	db *pgxpool.Pool,
	rds *goredis.Client,
	rateLimiter *ratelimit.Bucket,
	l1Registry *cacheengine.CacheRegistry,
	l1Fanout *cacheengine.RedisFanout,
) (*Module, error) {
	if cfg == nil {
		return nil, fmt.Errorf("core module: config is required")
	}
	if db == nil {
		return nil, fmt.Errorf("core module: database pool is required")
	}

	// 1) SoT data access for secret lifecycle.
	repo := coreRepoImpl.NewSecretRepository(cfg, db)
	readService := coreSvcImpl.NewSecretReadService(repo)
	// 2) Runtime provider for low-latency reads with cache-aside policy.
	provider := coreCache.NewCacheAsideSecretProviderWithTTL(readService, cfg.Security.SecretCacheTTL)
	// 3) Rotation + cache invalidation orchestration.
	bus := coreCache.NewRedisSecretInvalidationBus(rds, provider, cfg.App.AppName)
	rotationService := coreSvcImpl.NewSecretRotationService(repo, bus)
	zoneRepo := coreRepoImpl.NewZoneRepoImpl(cfg, db)
	if zoneRepo == nil {
		return nil, fmt.Errorf("core module: zone service unavailable: zone repository is nil")
	}
	zoneService := coreSvcImpl.NewZoneService(zoneRepo, l1Registry, l1Fanout)
	zoneHandler := coreHandler.NewZoneHandler(zoneService)
	if zoneHandler == nil {
		return nil, fmt.Errorf("core module: zone handler is nil")
	}

	// 6) Dataplane dependencies injection
	dataplaneNodeRepo := coreRepoImpl.NewDataplaneNodeRepoImpl(cfg, db)
	if dataplaneNodeRepo == nil {
		return nil, fmt.Errorf("core module: dataplane service unavailable: dataplane repository is nil")
	}
	dataplaneCache := coreCache.NewDataplaneCacheImpl(rds)
	if dataplaneCache == nil {
		return nil, fmt.Errorf("core module: dataplane service unavailable: dataplane cache is nil")
	}
	dataplaneNodeService := coreSvcImpl.NewDataplaneNodeService(dataplaneNodeRepo, dataplaneCache, zoneRepo)
	if dataplaneNodeService == nil {
		return nil, fmt.Errorf("core module: dataplane service unavailable: dataplane service is nil")
	}
	dataplaneOrchestrator := coreSvcImpl.NewDataplaneOrchestrator(dataplaneNodeRepo, dataplaneCache, dataplaneNodeService)
	if dataplaneOrchestrator == nil {
		return nil, fmt.Errorf("core module: dataplane service unavailable: dataplane orchestrator is nil")
	}
	dataplaneHeartbeatSubscriber := coreSvcImpl.NewDataplaneHeartbeatSubscriber(dataplaneCache, dataplaneNodeService)
	if dataplaneHeartbeatSubscriber == nil {
		return nil, fmt.Errorf("core module: dataplane service unavailable: dataplane subscriber is nil")
	}

	m := &Module{
		cfg:                          cfg,
		SecretRepository:             repo,
		SecretRotationService:        rotationService,
		SecretReadService:            readService,
		RuntimeSecretProvider:        provider,
		ZoneRepository:               zoneRepo,
		ZoneService:                  zoneService,
		ZoneHandler:                  zoneHandler,
		DataplaneNodeRepository:      dataplaneNodeRepo,
		DataplaneNodeService:         dataplaneNodeService,
		DataplaneOrchestrator:        dataplaneOrchestrator,
		DataplaneHeartbeatSubscriber: dataplaneHeartbeatSubscriber,
		invalidationBus:              bus,
		rateLimiter:                  rateLimiter,
		L1Registry:                   l1Registry,
	}

	return m, nil
}

// RegisterGRPCServices phơi ra phương thức đăng ký grpc services phục vụ app bootstrap layer.
func (m *Module) RegisterGRPCServices(server *grpc.Server) {
	if m == nil || m.DataplaneNodeService == nil {
		return
	}
	handler := coreRpcHandler.NewDataplaneGRPCHandler(m.DataplaneNodeService)
	coreProto.RegisterDataplaneRegistryServiceServer(server, handler)
	logger.SysInfo("grpc", "registered DataplaneRegistryService onto gRPC server")
}

// Bootstrap khởi tạo các side-effect lâu dài và chạy các background task của module Core.
func (m *Module) Bootstrap(ctx context.Context) error {
	if m == nil || m.SecretRotationService == nil {
		return nil
	}
	if m.invalidationBus != nil && m.listenCancel == nil {
		listenCtx, cancel := context.WithCancel(ctx)
		m.listenCancel = cancel
		go func() {
			if err := m.invalidationBus.Listen(listenCtx); err != nil && err != context.Canceled {
				logger.SysWarn("core", err.Error())
			}
		}()
	}
	if m.DataplaneOrchestrator != nil && m.orchestratorCancel == nil {
		orchCtx, cancel := context.WithCancel(ctx)
		m.orchestratorCancel = cancel
		go m.DataplaneOrchestrator.Start(orchCtx)
	}
	if m.DataplaneHeartbeatSubscriber != nil && m.subscriberCancel == nil {
		subCtx, cancel := context.WithCancel(ctx)
		m.subscriberCancel = cancel
		go m.DataplaneHeartbeatSubscriber.Start(subCtx)
	}

	families := []coreEntity.BootstrapSecretFamily{
		{Code: "access_token", Name: "Access Token", Description: "Primary signing secret for user access tokens."},
		{Code: "refresh_token", Name: "Refresh Token", Description: "Primary signing secret for refresh token flows."},
		{Code: "admin_api_key", Name: "Admin API Key", Description: "Primary secret family for admin API key related flows."},
		{Code: "one_time_token", Name: "One Time Token", Description: "Primary secret family for one-time token issuance and verification."},
	}
	for _, family := range families {
		if _, err := m.SecretRotationService.EnsureInitialSecretVersion(ctx, family); err != nil {
			return err
		}
	}
	return nil
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
	if m.orchestratorCancel != nil {
		m.orchestratorCancel()
		m.orchestratorCancel = nil
	}
	if m.subscriberCancel != nil {
		m.subscriberCancel()
		m.subscriberCancel = nil
	}
}
