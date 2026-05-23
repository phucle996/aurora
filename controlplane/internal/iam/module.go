package iam

import (
	"context"
	infraredis "controlplane/infra/redis"
	"controlplane/infra/telegram"
	"controlplane/internal/config"
	iamCache "controlplane/internal/iam/cache"
	coreSvc "controlplane/internal/iam/domain/service"
	iamRepoImpl "controlplane/internal/iam/repository"
	iamSvcImpl "controlplane/internal/iam/service"
	iamHandler "controlplane/internal/iam/transport/http/handler"
	"controlplane/internal/ratelimit"
	"controlplane/internal/security"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

type Module struct {
	cfg            *config.Config
	db             *pgxpool.Pool
	rds            *goredis.Client
	rateLimiter    *ratelimit.Bucket
	secretProvider security.SecretProvider

	AuthHandler         *iamHandler.AuthHandler
	LogoutHandler       *iamHandler.LogoutHandler
	RefreshTokenHandler *iamHandler.RefreshTokenHandler
	DeviceHandler       *iamHandler.DeviceHandler
	AdminAuthHandler    *iamHandler.AdminAuthHandler
	RbacHandler         *iamHandler.RbacHandler
	adminAPIKeyService  coreSvc.AdminAPIKeyService
	userDeviceRuntime   iamCache.UserDeviceRuntimeCache
	rbacSync            *iamSvcImpl.RbacCacheSync
	rotationCancel      context.CancelFunc
	finalizeCancel      context.CancelFunc
	deviceCapCancel     context.CancelFunc
	authSvcImpl         *iamSvcImpl.AuthService
}

func NewModule(cfg *config.Config, db *pgxpool.Pool, rds *goredis.Client, rateLimiter *ratelimit.Bucket, secretProvider security.SecretProvider) (*Module, error) {
	if cfg == nil {
		return nil, errors.New("iam module: config is required")
	}
	if rds == nil {
		return nil, errors.New("iam module: redis client is required")
	}
	authRepo := iamRepoImpl.NewAuthRepository(cfg, db)
	deviceRepo := iamRepoImpl.NewDeviceRepository(cfg, db)
	userDeviceRuntime := iamCache.NewUserDeviceRuntimeCache(rds)
	refreshTokenRepo := iamRepoImpl.NewRefreshTokenRepository(cfg, db)
	oneTimeTokenSvc := iamSvcImpl.NewOneTimeTokenService(cfg, iamCache.NewOneTimeTokenCache(rds))
	streamPublisher := infraredis.NewRedisStreamPublisher(rds)
	capLock := iamCache.NewUserDeviceCapLock(rds)
	authSvcImpl := iamSvcImpl.NewAuthServiceImpl(cfg, authRepo, refreshTokenRepo, deviceRepo, userDeviceRuntime, capLock, iamCache.NewRegisterPresenceCache(rds), secretProvider, oneTimeTokenSvc, streamPublisher)
	authSvc := iamSvcImpl.WrapAuthService(authSvcImpl)
	authHandler := iamHandler.NewAuthHandler(cfg, authSvc)
	logoutHandler := iamHandler.NewLogoutHandler(cfg, authSvc)

	refreshTokenSvc := iamSvcImpl.NewRefreshTokenService(cfg, refreshTokenRepo, userDeviceRuntime, secretProvider)
	refreshTokenHandler := iamHandler.NewRefreshTokenHandler(cfg, refreshTokenSvc)
	deviceSvc := iamSvcImpl.NewDeviceService(deviceRepo, refreshTokenRepo, userDeviceRuntime, streamPublisher)
	deviceHandler := iamHandler.NewDeviceHandler(deviceSvc)

	adminRepo := iamRepoImpl.NewAdminAPIKeyRepository(cfg, db)
	adminDeviceRuntime := iamCache.NewAdminDeviceRuntimeCache(rds)
	adminRotateTrigger := iamCache.NewAdminKeyRotationTriggerCache(rds)
	tgClient := telegram.NewTelegramClient(cfg.Telegram.BotToken, cfg.Telegram.ChatID)
	adminSvc := iamSvcImpl.NewAdminAPIKeyService(cfg, adminRepo, tgClient, secretProvider, adminDeviceRuntime, iamCache.NewAdminAPIKeyCache(rds), adminRotateTrigger)
	adminAuthHandler := iamHandler.NewAdminAuthHandler(cfg, adminSvc)
	rbacRepo := iamRepoImpl.NewRbacRepository(cfg, db)
	rbacRegistry := iamSvcImpl.NewRoleRegistry(15 * time.Minute)
	rbacBus := iamCache.NewRedisRbacCacheBus(rds)
	rbacSvc := iamSvcImpl.NewRbacService(rbacRepo, rbacRegistry, rbacBus)
	rbacSync := iamSvcImpl.NewRbacCacheSync(iamCache.NewRedisRbacSyncStore(rds), rbacRegistry)
	rbacHandler := iamHandler.NewRbacHandler(rbacSvc)

	return &Module{
		cfg:                 cfg,
		db:                  db,
		rds:                 rds,
		rateLimiter:         rateLimiter,
		secretProvider:      secretProvider,
		AuthHandler:         authHandler,
		authSvcImpl:         authSvcImpl,
		LogoutHandler:       logoutHandler,
		RefreshTokenHandler: refreshTokenHandler,
		DeviceHandler:       deviceHandler,
		AdminAuthHandler:    adminAuthHandler,
		RbacHandler:         rbacHandler,
		adminAPIKeyService:  adminSvc,
		userDeviceRuntime:   userDeviceRuntime,
		rbacSync:            rbacSync,
	}, nil
}
