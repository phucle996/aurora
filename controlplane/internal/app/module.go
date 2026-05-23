package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"controlplane/internal/config"
	"controlplane/internal/core"
	healthhandler "controlplane/internal/http/handler"
	"controlplane/internal/http/middleware"
	"controlplane/internal/iam"
	iamCache "controlplane/internal/iam/cache"
	iamRepoImpl "controlplane/internal/iam/repository"
	"controlplane/internal/ratelimit"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

type Modules struct {
	// Health là global health/readiness surface của process.
	Health *healthhandler.HealthHandler
	// Core là module hạ tầng lõi (secrets/zone...).
	Core *core.Module
	// IAM là module authn/authz của controlplane.
	IAM *iam.Module
}

// NewGlobalModules khởi tạo toàn bộ global modules và health dependencies.
//
// Trách nhiệm:
// - tạo Health handler,
// - khởi động time-drift probe read-only (global observability concern),
// - khởi tạo Core module,
// - khởi tạo IAM module (phụ thuộc SecurityProvider của Core),
// - mark health ready khi module graph đã dựng xong.
//
// Lưu ý:
//   - TimeSync probe ở đây chỉ cập nhật tín hiệu drift cho health/metrics,
//     không can thiệp chỉnh clock hệ điều hành.
func NewGlobalModules(cfg *config.Config,
	db *pgxpool.Pool,
	rds *goredis.Client,
	rateLimiter *ratelimit.Bucket,
) (*Modules, error) {
	health := healthhandler.NewHealthHandler(db, rds)

	// Global time drift probe (read-only):
	// - probe loop đọc drift state,
	// - ticker loop đẩy snapshot vào health surface.
	probe := NewTimeSyncProbe()
	go probe.Start(context.Background())

	go func() {
		tk := time.NewTicker(30 * time.Second)
		defer tk.Stop()
		for {
			s := probe.Snapshot()
			health.SetTimeDrift(s.Seconds, string(s.State))
			<-tk.C
		}
	}()

	coreModule, err := core.NewModule(cfg, db, rds)
	if err != nil {
		return nil, fmt.Errorf("app: init core module: %w", err)
	}

	iamModule, err := iam.NewModule(cfg, db, rds, rateLimiter, coreModule.SecurityProvider)
	if err != nil {
		return nil, fmt.Errorf("app: init iam module: %w", err)
	}

	if err := initMiddlewares(cfg, db, coreModule, rds); err != nil {
		return nil, err
	}

	health.MarkReady()

	return &Modules{
		Health: health,
		Core:   coreModule,
		IAM:    iamModule,
	}, nil
}

func initMiddlewares(cfg *config.Config, db *pgxpool.Pool, coreModule *core.Module, rds *goredis.Client) error {
	if cfg == nil {
		return errors.New("app: init middleware: config is required")
	}
	if db == nil {
		return errors.New("app: init middleware: database is required")
	}
	if coreModule == nil || coreModule.SecurityProvider == nil {
		return errors.New("app: init middleware: core security provider is required")
	}
	if rds == nil {
		return errors.New("app: init middleware: redis client is required")
	}
	middleware.InitAdminCIDR(cfg.Security.AdminAllowedCIDRs)
	adminDeviceRuntime := iamCache.NewAdminDeviceRuntimeCache(rds)
	adminRotateTrigger := iamCache.NewAdminKeyRotationTriggerCache(rds)
	if err := middleware.InitAdminAPIKeyAuth(
		coreModule.SecurityProvider,
		adminDeviceRuntime.VerifyDeviceSecret,
		adminRotateTrigger.SetRotationRequired,
	); err != nil {
		return fmt.Errorf("app: init admin api key middleware: %w", err)
	}
	adminRepo := iamRepoImpl.NewAdminAPIKeyRepository(cfg, db)
	if err := middleware.InitAdminCriticalSignature(
		func(ctx context.Context, deviceID string) (string, error) {
			// deviceID ở đây là runtime device id trong admin cookie/JWT.
			// Public key source-of-truth cho session nằm trong Redis runtime để
			// critical action không phải query DB trên từng request.
			runtimeRecord, err := adminDeviceRuntime.GetDeviceRuntime(ctx, deviceID)
			if err != nil {
				return "", err
			}
			if runtimeRecord == nil {
				return "", nil
			}
			if pubKey := strings.TrimSpace(runtimeRecord.DevicePublicKey); pubKey != "" {
				return pubKey, nil
			}

			// Backward compatibility cho runtime record cũ chưa có device_public_key:
			// dùng tracked admin_devices.id để fallback DB trong phần còn lại của
			// session hiện tại.
			trackedDeviceID := strings.TrimSpace(runtimeRecord.TrackedDeviceID)
			if trackedDeviceID == "" {
				return "", nil
			}
			device, err := adminRepo.GetAdminDeviceByID(ctx, trackedDeviceID)
			if err != nil || device == nil {
				return "", err
			}
			return device.PublicKey, nil
		},
		rds,
		time.Minute,
		2*time.Minute,
	); err != nil {
		return fmt.Errorf("app: init admin critical signature middleware: %w", err)
	}
	if err := middleware.InitAdminCriticalStepUp2FA(func(ctx context.Context) (string, time.Time, error) {
		return loadAdminStepUp2FASettings(ctx, rds, func(ctx context.Context) (string, time.Time, error) {
			settings, err := adminRepo.GetAdmin2FASettings(ctx)
			if err != nil || settings == nil {
				return "", time.Time{}, err
			}
			return settings.SecretCiphertext, settings.UpdatedAt, nil
		})
	}); err != nil {
		return fmt.Errorf("app: init admin critical step-up middleware: %w", err)
	}
	return nil
}

// Stop dừng toàn bộ modules theo thứ tự an toàn và nil-safe.
//
// Thứ tự hiện tại:
// 1) mark health not-ready,
// 2) stop IAM module,
// 3) stop Core module.
func (m *Modules) Stop() {
	if m == nil {
		return
	}
	if m.Health != nil {
		m.Health.MarkNotReady()
	}
	if m.IAM != nil {
		m.IAM.Stop()
	}
	if m.Core != nil {
		m.Core.Stop()
	}
}
