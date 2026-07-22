package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	"controlplane/internal/hierarchy"
	healthhandler "controlplane/internal/http/handler"
	"controlplane/internal/hypervisor"
	"controlplane/internal/iam"
	"controlplane/internal/mail"
	"controlplane/internal/observability"
	"controlplane/internal/storage"
	"controlplane/pkg/logger"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
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
	// Storage là module vệ tinh Tier-2 (lưu trữ object). Cho phép chạy ở trạng thái suy giảm (Degraded).
	Storage *storage.StorageModule
	// L1Registry là bộ đăng ký in-memory cache L1 tĩnh.
	CacheEngine *cacheengine.CacheRegistry
	// DeltaEngine điều phối đồng bộ động cấu hình trong RAM, DB, NATS.
	probeCancel context.CancelFunc
}

// NewGlobalModules là điểm dựng module graph ở app-layer và là nơi fail-fast
// chính cho bootstrap cross-module.
func NewGlobalModules(cfg *config.Config,
	db *pgxpool.Pool,
	rds *goredis.Client,
	rdsJob *goredis.Client,
	cacheEngine *cacheengine.CacheRegistry,
	natsConn *nats.Conn,
	otel *observability.OTel,
) (*Modules, error) {
	// ------------------------------------------------------------------------
	// GIAI ĐOẠN 1: KHỞI TẠO HỆ THỐNG GIÁM SÁT & OBSERVABILITY
	// ------------------------------------------------------------------------

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

	// ------------------------------------------------------------------------
	// GIAI ĐOẠN 2: KHỞI TẠO CÁC PHÂN HỆ TIER-0 (CRITICAL) - SAI LÀ FAIL-FAST
	// ------------------------------------------------------------------------

	// 3) Core module bootstrap: source runtime provider cho secrets/security.
	coreModule, err := core.NewModule(cfg, db, rds, cacheEngine, natsConn, otel)
	if err != nil {
		return nil, fmt.Errorf("app: init critical core module: %w", err)
	}
	if coreModule == nil {
		return nil, errors.New("app: init critical core module: core module is nil")
	}

	// 5) IAM module bootstrap phụ thuộc l1 cache registry.
	iamModule, err := iam.NewModule(cfg, db, rds, rdsJob, cacheEngine, natsConn, otel)
	if err != nil {
		return nil, fmt.Errorf("app: init critical iam module: %w", err)
	}
	if iamModule == nil {
		return nil, errors.New("app: init critical iam module: iam module is nil")
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
	mailModule, err := mail.NewModule(cfg, db)
	if err != nil {
		logger.SysError("graceful.degradation.mail", fmt.Sprintf("Failed to initialize mail module: %v. Running in degraded mode.", err))
		mailModule = mail.NewDegradedModule(err)
	}

	// [COMMENT]: Khởi tạo phân hệ Storage (Tier 2). Hỗ trợ chạy ở chế độ suy giảm (Degraded Mode).
	storageModule, err := storage.NewModule(cfg, db, rds, cacheEngine, natsConn)
	if err != nil {
		logger.SysError("graceful.degradation.storage", fmt.Sprintf("Failed to initialize storage module: %v. Running in degraded mode.", err))
		storageModule = storage.NewDegradedModule(err)
	}

	// 8) Chỉ mark ready khi toàn bộ module graph đã dựng xong.
	health.MarkReady()
	keepProbeRunning = true

	modules := &Modules{
		Health:      health,
		Core:        coreModule,
		IAM:         iamModule,
		Hypervisor:  hypervisorModule,
		Mail:        mailModule,
		Storage:     storageModule,
		CacheEngine: cacheEngine,
		probeCancel: probeCancel,
	}

	return modules, nil
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
	if m.Storage != nil {
		m.Storage.Stop()
	}
	if m.Core != nil {
		m.Core.Stop()
	}
	if m.CacheEngine != nil && m.CacheEngine.L1 != nil {
		m.CacheEngine.L1.Close()
	}
}
