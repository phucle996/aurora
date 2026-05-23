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
	"controlplane/internal/security"
	"controlplane/pkg/logger"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

type Module struct {
	cfg                   *config.Config
	SecretRepository      coreRepoInterface.SecretRepository
	SecretRotationService coreSvcInterface.SecretRotationService
	SecretReadService     coreSvcInterface.SecretReadService
	RuntimeSecretProvider coreSvcInterface.RuntimeSecretProvider
	SecurityProvider      security.SecretProvider
	ZoneRepository        coreRepoInterface.ZoneRepository
	ZoneService           coreSvcInterface.ZoneService
	ZoneHandler           *coreHandler.ZoneHandler
	invalidationBus       *coreCache.RedisSecretInvalidationBus
	listenCancel          context.CancelFunc
}

func NewModule(cfg *config.Config, db *pgxpool.Pool, rds *goredis.Client) (*Module, error) {
	repo := coreRepoImpl.NewSecretRepository(cfg, db)
	readService := coreSvcImpl.NewSecretReadService(repo)
	provider := coreSvcImpl.NewCacheAsideSecretProviderWithTTL(readService, cfg.Security.SecretCacheTTL)
	bus := coreCache.NewRedisSecretInvalidationBus(rds, provider, cfg.App.NodeID)
	rotationService := coreSvcImpl.NewSecretRotationServiceWithNotifier(repo, bus)
	securityProvider := coreSvcImpl.NewSecuritySecretProvider(provider)

	// zone dependencies injection
	zoneRepo := coreRepoImpl.NewZoneRepoImpl(cfg, db)
	zoneService := coreSvcImpl.NewZoneService(zoneRepo)
	zoneHandler := coreHandler.NewZoneHandler(zoneService)
	return &Module{
		cfg:                   cfg,
		SecretRepository:      repo,
		SecretRotationService: rotationService,
		SecretReadService:     readService,
		RuntimeSecretProvider: provider,
		SecurityProvider:      securityProvider,
		ZoneRepository:        zoneRepo,
		ZoneService:           zoneService,
		ZoneHandler:           zoneHandler,
		invalidationBus:       bus,
	}, nil
}

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

func (m *Module) Stop() {
	if m == nil {
		return
	}
	if m.listenCancel != nil {
		m.listenCancel()
		m.listenCancel = nil
	}
}
