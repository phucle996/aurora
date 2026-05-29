package core

import (
	"context"
	"controlplane/internal/config"
	coreCache "controlplane/internal/core/cache"
	coreEntity "controlplane/internal/core/domain/entity"
	coreRepoInterface "controlplane/internal/core/domain/repo"
	coreSvcInterface "controlplane/internal/core/domain/service"
	coreRepoImpl "controlplane/internal/core/repository"
	coreSvcImpl "controlplane/internal/core/service"
	coreHandler "controlplane/internal/core/transport/http/handler"
	"controlplane/pkg/logger"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

type Module struct {
	cfg                   *config.Config
	SecretRepository      coreRepoInterface.SecretRepository
	SecretRotationService coreSvcInterface.SecretRotationService
	SecretReadService     coreSvcInterface.SecretReadService
	RuntimeSecretProvider coreSvcInterface.RuntimeSecretProvider
	ZoneRepository        coreRepoInterface.ZoneRepository
	ZoneService           coreSvcInterface.ZoneService
	ZoneHandler           *coreHandler.ZoneHandler
	DataplaneNodeRepository coreRepoInterface.DataplaneNodeRepository
	DataplaneNodeService    coreSvcInterface.DataplaneNodeService
	DataplaneOrchestrator   *coreSvcImpl.DataplaneOrchestrator
	invalidationBus       *coreCache.RedisSecretInvalidationBus
	listenCancel          context.CancelFunc
	orchestratorCancel    context.CancelFunc
}

// NewModule dựng dependency graph của Core và trả về lỗi wiring/bootstrap để
// caller ở app-layer quyết định policy warning/fatal.
//
// Contract ổn định:
// - Core phải cung cấp được secret runtime + security provider cho các module khác.
// - Lỗi dependency bắt buộc (config/db/security provider) phải return error sớm.
// - Không panic trong flow bình thường; quyết định dừng app nằm ở app module.
func NewModule(cfg *config.Config, db *pgxpool.Pool, rds *goredis.Client) (*Module, error) {
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
	// 5) Zone dependencies injection.
	zoneRepo := coreRepoImpl.NewZoneRepoImpl(cfg, db)
	if zoneRepo == nil {
		return nil, fmt.Errorf("core module: zone service unavailable: zone repository is nil")
	}
	zoneService := coreSvcImpl.NewZoneService(zoneRepo)
	if zoneService == nil {
		return nil, fmt.Errorf("core module: zone service unavailable: zone service is nil")
	}
	zoneHandler := coreHandler.NewZoneHandler(zoneService)
	if zoneHandler == nil {
		return nil, fmt.Errorf("core module: zone service unavailable: zone handler is nil")
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
	dataplaneOrchestrator := coreSvcImpl.NewDataplaneOrchestrator(dataplaneNodeRepo, dataplaneCache)
	if dataplaneOrchestrator == nil {
		return nil, fmt.Errorf("core module: dataplane service unavailable: dataplane orchestrator is nil")
	}

	return &Module{
		cfg:                     cfg,
		SecretRepository:        repo,
		SecretRotationService:   rotationService,
		SecretReadService:       readService,
		RuntimeSecretProvider:   provider,
		ZoneRepository:          zoneRepo,
		ZoneService:             zoneService,
		ZoneHandler:             zoneHandler,
		DataplaneNodeRepository: dataplaneNodeRepo,
		DataplaneNodeService:    dataplaneNodeService,
		DataplaneOrchestrator:   dataplaneOrchestrator,
		invalidationBus:         bus,
	}, nil
}

// Bootstrap khởi tạo các side-effect lâu dài của module:
// - Listen global invalidation bus.
// - Ensure 4 initial secret families.
//
// Lỗi Bootstrap được trả về để app-layer quyết định shutdown.
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

// Stop hủy các background goroutine của module.
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
}
