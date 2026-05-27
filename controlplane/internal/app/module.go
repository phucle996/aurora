package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"controlplane/internal/config"
	"controlplane/internal/core"
	coreSvcImpl "controlplane/internal/core/service"
	healthhandler "controlplane/internal/http/handler"
	"controlplane/internal/http/middleware"
	"controlplane/internal/iam"
	iamCache "controlplane/internal/iam/cache"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamRepoImpl "controlplane/internal/iam/repository"
	"controlplane/internal/policyengine"
	"controlplane/internal/security"
	"controlplane/internal/security/ratelimit"

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
	// PolicyEngine là runtime hot-reload module cho policies.
	PolicyEngine *policyengine.Engine
	probeCancel  context.CancelFunc
}

// NewGlobalModules là điểm dựng module graph ở app-layer và là nơi fail-fast
// chính cho bootstrap cross-module.
//
// CONTRACT:
//   - Dựng theo thứ tự: Core -> security adapter -> IAM -> middleware wiring.
//   - Chỉ `health.MarkReady()` khi toàn bộ graph dựng thành công.
//   - Bất kỳ lỗi ở dependency bắt buộc/cross-module phải return error ngay để
//     caller (NewApplication/main) quyết định dừng app.
//
// BOUNDARY:
// - Hàm này chỉ làm wiring + lifecycle bootstrap giữa các module.
// - Không chứa business policy của từng module (IAM/Core/domain).
// - Không tự degrade âm thầm các dependency bắt buộc.
//
// NOTES:
// - Time drift probe ở đây là read-only observability concern.
// - Security adapter được dựng tại app-layer để giảm magic trong core module.
// - Đây là nơi tập trung nhất quán fail-fast cho lỗi liên quan nhiều module.
func NewGlobalModules(cfg *config.Config,
	db *pgxpool.Pool,
	rds *goredis.Client,
	rateLimiter *ratelimit.Bucket,
	policyEngineModule *policyengine.Engine,
) (*Modules, error) {
	// 1) Global health surface.
	health := healthhandler.NewHealthHandler(db, rds)

	// 2) Time drift probe read-only: chỉ ghi tín hiệu health/metrics, không chỉnh clock OS.
	probe := NewTimeSyncProbe()
	probeCtx, probeCancel := context.WithCancel(context.Background())
	keepProbeRunning := false
	defer func() {
		if !keepProbeRunning {
			probeCancel()
		}
	}()
	go probe.Start(probeCtx)

	go func() {
		tk := time.NewTicker(30 * time.Second)
		defer tk.Stop()
		for {
			select {
			case <-probeCtx.Done():
				return
			case <-tk.C:
				s := probe.Snapshot()
				health.SetTimeDrift(s.Seconds, string(s.State))
			}
		}
	}()

	// 3) Core module bootstrap: source runtime provider cho secrets/security.
	coreModule, err := core.NewModule(cfg, db, rds)
	if err != nil {
		return nil, fmt.Errorf("app: init core module: %w", err)
	}
	if coreModule == nil || coreModule.RuntimeSecretProvider == nil {
		return nil, errors.New("app: init core module: runtime secret provider is required")
	}
	// 4) Adapter runtime provider -> security provider để cấp cho IAM/middlewares.
	securityProvider := coreSvcImpl.NewSecuritySecretProvider(coreModule.RuntimeSecretProvider)

	// 5) IAM module bootstrap phụ thuộc security provider từ core runtime.
	iamModule, err := iam.NewModule(cfg, db, rds, rateLimiter, securityProvider)
	if err != nil {
		return nil, fmt.Errorf("app: init iam module: %w", err)
	}

	// 6) Policy engine bootstrap (Được truyền từ ngoài vào như hạ tầng hệ thống)
	if policyEngineModule == nil {
		return nil, errors.New("app: init policy engine module: engine service is required")
	}

	// 7) Global middleware bootstrap (cross-module wiring).
	if err := initMiddlewares(cfg, db, coreModule, securityProvider, rds, policyEngineModule); err != nil {
		return nil, err
	}

	// 8) Chỉ mark ready khi toàn bộ module graph đã dựng xong.
	health.MarkReady()
	keepProbeRunning = true

	return &Modules{
		Health:       health,
		Core:         coreModule,
		IAM:          iamModule,
		PolicyEngine: policyEngineModule,
		probeCancel:  probeCancel,
	}, nil
}

func initMiddlewares(cfg *config.Config, db *pgxpool.Pool, coreModule *core.Module, securityProvider security.SecretProvider, rds *goredis.Client, policyModule *policyengine.Engine) error {
	if cfg == nil {
		return errors.New("app: init middleware: config is required")
	}
	if db == nil {
		return errors.New("app: init middleware: database is required")
	}
	if coreModule == nil || coreModule.RuntimeSecretProvider == nil {
		return errors.New("app: init middleware: core runtime secret provider is required")
	}
	if securityProvider == nil {
		return errors.New("app: init middleware: security provider is required")
	}
	if rds == nil {
		return errors.New("app: init middleware: redis client is required")
	}

	policySnapshot, err := policyModule.EngineService.Current(context.Background())
	if err != nil || policySnapshot == nil {
		return errors.New("app: init middleware: active runtime policy is required")
	}
	middleware.InitAdminCIDR(policySnapshot.Runtime.AdminCIDR.Allowlist)
	middleware.InitRateLimitPolicy(policySnapshot.Runtime.RateLimit)
	middleware.InitAccess(
		securityProvider,
		rds,
		iamCache.NewUserDeviceRuntimeCache(rds),
		10*time.Second,
	)
	adminDeviceRuntime := iamCache.NewAdminDeviceRuntimeCache(rds)
	adminRotateTrigger := iamCache.NewAdminKeyRotationTriggerCache(rds)
	if err := middleware.InitAdminAPIKeyAuth(
		securityProvider,
		adminDeviceRuntime.VerifyDeviceSecret,
		adminRotateTrigger.SetRotationRequired,
	); err != nil {
		return fmt.Errorf("app: init admin api key middleware: %w", err)
	}
	adminRepo := iamRepoImpl.NewAdminAPIKeyRepository(cfg, db)
	if err := middleware.InitAdminCriticalSignature(
		buildAdminPublicKeyResolver(adminDeviceRuntime, adminRepo),
		rds,
		time.Minute,
		2*time.Minute,
	); err != nil {
		return fmt.Errorf("app: init admin critical signature middleware: %w", err)
	}
	stepUpLoader := iamCache.AdminStepUp2FASecretLoaderFunc(func(ctx context.Context) (string, time.Time, error) {
		return iamCache.LoadAdminStepUp2FASettings(ctx, rds, func(ctx context.Context) (string, time.Time, error) {
			settings, err := adminRepo.GetAdmin2FASettings(ctx)
			if err != nil || settings == nil {
				return "", time.Time{}, err
			}
			return settings.SecretCiphertext, settings.UpdatedAt, nil
		})
	})
	if err := middleware.InitAdminCriticalStepUp2FA(stepUpLoader); err != nil {
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
	if m.probeCancel != nil {
		m.probeCancel()
		m.probeCancel = nil
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
	if m.PolicyEngine != nil {
		m.PolicyEngine.Stop()
	}
}

func buildAdminPublicKeyResolver(adminDeviceRuntime iamCache.AdminDeviceRuntimeCache, adminRepo iamRepoInterface.AdminAPIKeyRepository) func(ctx context.Context, accessKey string) (string, error) {
	return func(ctx context.Context, accessKey string) (string, error) {
		runtimeRecord, err := adminDeviceRuntime.GetDeviceRuntime(ctx, accessKey)
		if err != nil {
			return "", err
		}
		if runtimeRecord == nil {
			return "", nil
		}
		if pubKey := strings.TrimSpace(runtimeRecord.DevicePublicKey); pubKey != "" {
			return pubKey, nil
		}
		trackedDeviceID := strings.TrimSpace(runtimeRecord.TrackedDeviceID)
		if trackedDeviceID == "" {
			return "", nil
		}
		device, err := adminRepo.GetAdminDeviceByID(ctx, trackedDeviceID)
		if err != nil || device == nil {
			return "", err
		}
		return device.PublicKey, nil
	}
}
