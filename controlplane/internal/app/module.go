// ============================================================================
// 🏛️ ARCHITECTURAL COMPONENT: GLOBAL APPLICATION GRAPH & DI CONTAINER (SOT)
// ============================================================================
// Thiết kế bởi: Antigravity AI & SRE Platform Engineering Team.
//
// 📜 SOVEREIGN CONTRACT (Hợp đồng Tối cao) & CHỨC NĂNG CHÍNH:
//   - Tệp tin này là **SOURCE OF TRUTH (SoT) DUY NHẤT** kiến tạo nên toàn bộ đồ thị phụ thuộc
//     (Dependency Graph - DI) của ứng dụng Control Plane.
//   - Chức năng cốt lõi: Thiết lập trật tự khởi dựng, kết nối các luồng phụ thuộc chéo giữa
//     các module độc lập, quản lý vòng đời (Lifecycle) và phân phối tài nguyên hệ thống
//     (Database Pool, Redis Client, Ratelimiter) cho các module thành phần.
//
// 🛡️ SRE HA ARCHITECTURAL BOUNDARY (Ranh giới Phân loại & Phục hồi Sự cố):
//   Hệ thống khởi dựng được phân hoạch thành hai phân hạng rõ rệt nhằm đạt tính HA tối đa:
//
//   🟢 PHÂN HẠNG 1: TIER-0 (CRITICAL DEPENDENCIES - SAI LÀ FAIL-FAST TOÀN CỤC)
//     - Các module: `Core`, `IAM`, `PolicyEngine`, `Health`, và các Global Middlewares.
//     - Chính sách: Nếu xảy ra bất kỳ lỗi khởi dựng nào ở các cấu phần này, tiến trình
//       BẮT BUỘC phải dừng ngay lập tức (Fail-Fast) và trả lỗi về hàm main để Kubelet
//       phát hiện thông qua Startup Probe và đưa Pod vào vòng lặp cảnh báo (CrashLoopback).
//
//   🔵 PHÂN HẠNG 2: TIER-1 (NON-CRITICAL DEPENDENCIES - SAI LÀ DEGRADE CHỌN LỌC)
//     - Các module: `Hypervisor` (ảo hóa hệ thống), v.v.
//     - Chính sách: Lỗi kết nối API mạng, lỗi drift cấu hình hạ tầng của các phân hệ này
//       TUYỆT ĐỐI KHÔNG ĐƯỢC PHÉP làm sập Control Plane. Hệ thống phải thực hiện
//       **Graceful Degradation (Suy giảm tính năng chọn lọc)**:
//       1. Bắt lỗi (Catch Error) tại biên khởi dựng.
//       2. Ghi log cảnh báo mức hệ thống (observability alert).
//       3. Khởi tạo một phiên bản câm mang lỗi (Dummy Degraded Instance - Null Object Pattern)
//          để thay thế, bảo đảm không gây ra Nil Pointer Panic khi chạy các logic nghiệp vụ sau.
//       4. Vô hiệu hóa tính năng (Disable) cục bộ phân hệ đó và tiếp tục khởi động thành công ứng dụng.
// ============================================================================

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	"controlplane/internal/core"
	healthhandler "controlplane/internal/http/handler"
	"controlplane/internal/http/middleware"
	"controlplane/internal/hypervisor"
	"controlplane/internal/iam"
	"controlplane/internal/mail"
	"controlplane/internal/policyengine"
	policyRateLimit "controlplane/internal/policyengine/policies/ratelimit"
	"controlplane/internal/security/ratelimit"
	"controlplane/pkg/logger"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

type Modules struct {
	// Health là global health/readiness surface của process.
	Health *healthhandler.HealthHandler
	// Core là module hạ tầng lõi (secrets/zone...).
	Core *core.Module
	// IAM là module authn/authz của controlplane.
	IAM *iam.IAMModule
	// Hypervisor là module vệ tinh Tier-1 (ảo hóa). Cho phép chạy ở trạng thái suy giảm (Degraded).
	Hypervisor *hypervisor.HypervisorModule
	// Mail là module vệ tinh Tier-1 (gửi mail). Cho phép chạy ở trạng thái suy giảm (Degraded).
	Mail *mail.Module
	// PolicyEngine là runtime hot-reload module cho policies.
	PolicyEngine *policyengine.Engine
	// L1Registry là bộ đăng ký in-memory cache L1 tĩnh.
	L1Registry *cacheengine.CacheRegistry
	// DeltaEngine điều phối đồng bộ động cấu hình trong RAM, DB, NATS.
	probeCancel context.CancelFunc
}

// NewGlobalModules là điểm dựng module graph ở app-layer và là nơi fail-fast
// chính cho bootstrap cross-module.
func NewGlobalModules(cfg *config.Config,
	db *pgxpool.Pool,
	rdsCore *goredis.Client,
	rdsJob *goredis.Client,
	rateLimiter *ratelimit.Bucket,
	policyEngineModule *policyengine.Engine,
	l1Registry *cacheengine.CacheRegistry,
) (*Modules, error) {
	// ------------------------------------------------------------------------
	// GIAI ĐOẠN 1: KHỞI TẠO HỆ THỐNG GIÁM SÁT & OBSERVABILITY
	// ------------------------------------------------------------------------

	// 1) Global health surface.
	health := healthhandler.NewHealthHandler(db, rdsCore)

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

	// ------------------------------------------------------------------------
	// GIAI ĐOẠN 2: KHỞI TẠO CÁC PHÂN HỆ TIER-0 (CRITICAL) - SAI LÀ FAIL-FAST
	// ------------------------------------------------------------------------

	// 3) Core module bootstrap: source runtime provider cho secrets/security.
	coreModule, err := core.NewModule(cfg, db, rdsCore, rateLimiter, l1Registry)
	if err != nil {
		return nil, fmt.Errorf("app: init critical core module: %w", err)
	}
	if coreModule == nil {
		return nil, errors.New("app: init critical core module: core module is nil")
	}

	// 5) IAM module bootstrap phụ thuộc l1 cache registry.
	iamModule, err := iam.NewModule(cfg, db, rdsCore, rdsJob, rateLimiter, l1Registry)
	if err != nil {
		return nil, fmt.Errorf("app: init critical iam module: %w", err)
	}
	if iamModule == nil {
		return nil, errors.New("app: init critical iam module: iam module is nil")
	}

	// 6) Policy engine bootstrap (Được truyền từ ngoài vào như hạ tầng hệ thống)
	if policyEngineModule == nil {
		return nil, errors.New("app: init critical policy engine module: engine service is required")
	}

	// ------------------------------------------------------------------------
	// GIAI ĐOẠN 3: KHỞI TẠO CÁC PHÂN HỆ TIER-1 (NON-CRITICAL) - SAI LÀ DEGRADE GRACEFUL
	// ------------------------------------------------------------------------
	// SRE HA Warning: Lỗi kết nối, lỗi mạng hay lỗi cấu hình của phân hệ ảo hóa Hypervisor
	// tuyệt đối không được phép kéo sập ứng dụng. Bắt lỗi tại biên và degrade mượt mà.
	hypervisorModule, err := hypervisor.NewModule(cfg, db)
	if err != nil {
		// Log lỗi nghiêm trọng mức hệ thống phục vụ Alerting/Observability
		logger.SysError("graceful.degradation.hypervisor", fmt.Sprintf("Failed to initialize hypervisor module: %v. Running in degraded mode.", err))

		// Sử dụng Null Object Pattern (Dummy Degraded Module) để tránh Nil Pointer Panic sau này
		hypervisorModule = hypervisor.NewDegradedModule(err)
	}

	// SRE HA Warning: Lỗi kết nối, lỗi mạng hay lỗi cấu hình của phân hệ gửi mail Mail
	// tuyệt đối không được phép kéo sập ứng dụng. Bắt lỗi tại biên và degrade mượt mà.
	mailModule, err := mail.NewModule(cfg, db, rdsCore, rdsJob, rateLimiter, coreModule)
	if err != nil {
		logger.SysError("graceful.degradation.mail", fmt.Sprintf("Failed to initialize mail module: %v. Running in degraded mode.", err))
		mailModule = mail.NewDegradedModule(err)
	}

	// ------------------------------------------------------------------------
	// GIAI ĐOẠN 4: THIẾT LẬP MIDDLEWARES & AN TOÀN ĐỊNH TUYẾN TOÀN CỤC
	// ------------------------------------------------------------------------

	// 7) Global middleware bootstrap (cross-module wiring).
	if err := initMiddlewares(cfg, db, coreModule, rdsCore, policyEngineModule, l1Registry); err != nil {
		return nil, err
	}

	// 8) Chỉ mark ready khi toàn bộ module graph đã dựng xong.
	health.MarkReady()
	keepProbeRunning = true

	modules := &Modules{
		Health:       health,
		Core:         coreModule,
		IAM:          iamModule,
		Hypervisor:   hypervisorModule,
		Mail:         mailModule,
		PolicyEngine: policyEngineModule,
		L1Registry:   l1Registry,
		probeCancel:  probeCancel,
	}

	return modules, nil
}

func initMiddlewares(cfg *config.Config, db *pgxpool.Pool, coreModule *core.Module, rds *goredis.Client, policyModule *policyengine.Engine, cacheEngine *cacheengine.CacheRegistry) error {
	if cfg == nil {
		return errors.New("app: init middleware: config is required")
	}
	if db == nil {
		return errors.New("app: init middleware: database is required")
	}
	if coreModule == nil {
		return errors.New("app: init middleware: core module is required")
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

	policyModule.EngineService.RegisterRateLimitHook(func(policy *policyRateLimit.CompiledPolicy) {
		middleware.InitRateLimitPolicy(*policy)
	})
	middleware.InitZoneAuth(cacheEngine)
	middleware.InitAccess(cacheEngine, 10*time.Second)
	if err := middleware.InitAdminAPIKeyAuth(
		cacheEngine,
		func(ctx context.Context, accessKey string, accessSecret string) (bool, error) {
			return verifyAdminAccessSecret(ctx, cacheEngine.L2, accessKey, accessSecret)
		},
		func(ctx context.Context, ttl time.Duration) error {
			return setAdminRotationRequired(ctx, cacheEngine.L2, ttl)
		},
	); err != nil {
		return fmt.Errorf("app: init admin api key middleware: %w", err)
	}
	if err := middleware.InitAdminCriticalSignature(cacheEngine, time.Minute, 2*time.Minute); err != nil {
		return fmt.Errorf("app: init admin critical signature middleware: %w", err)
	}
	if err := middleware.InitAdminCriticalStepUp2FA(cacheEngine); err != nil {
		return fmt.Errorf("app: init admin critical step-up middleware: %w", err)
	}
	return nil
}

// Stop dừng toàn bộ modules theo thứ tự an toàn và nil-safe.
//
// Thứ tự hiện tại:
// 1) mark health not-ready,
// 2) stop IAM module,
// 3) stop Hypervisor module,
// 4) stop Core module.
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
	if m.Hypervisor != nil {
		m.Hypervisor.Stop()
	}
	if m.Mail != nil {
		_ = m.Mail.Stop(context.Background())
	}
	if m.Core != nil {
		m.Core.Stop()
	}
	if m.PolicyEngine != nil {
		m.PolicyEngine.Stop()
	}
	if m.L1Registry != nil && m.L1Registry.L1 != nil {
		m.L1Registry.L1.Close()
	}
}

func verifyAdminAccessSecret(ctx context.Context, l2 cacheengine.L2Cache, accessKey string, accessSecret string) (bool, error) {
	payload, _, exists, err := l2.Get(ctx, "admin_access_session:"+accessKey)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}

	var session struct {
		AccessSecretHash string `json:"access_secret_hash"`
	}
	if err := json.Unmarshal(payload, &session); err != nil {
		return false, err
	}

	h := sha256.Sum256([]byte(accessSecret))
	incomingHash := hex.EncodeToString(h[:])
	return session.AccessSecretHash == incomingHash, nil
}

func setAdminRotationRequired(ctx context.Context, l2 cacheengine.L2Cache, ttl time.Duration) error {
	return l2.Set(ctx, "iam:admin_key_rotation:required", "1", 1, ttl)
}
