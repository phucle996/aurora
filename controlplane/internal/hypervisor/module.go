package hypervisor

import (
	"context"
	"errors"
	"strings"

	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	hypervisorRepoInterface "controlplane/internal/hypervisor/domain/repo"
	hypervisorSvcInterface "controlplane/internal/hypervisor/domain/service"
	hypervisorRepoImpl "controlplane/internal/hypervisor/repository"
	hypervisorSvcImpl "controlplane/internal/hypervisor/service"
	hypervisorHandler "controlplane/internal/hypervisor/transport/http/handler"
	hypervisorStream "controlplane/internal/hypervisor/transport/stream"
	"controlplane/internal/observability"
	jobpayload "controlplane/internal/security"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

// HypervisorModule owns Proxmox-backed desired VM and image state. Physical
// node health remains an OTel/Grafana concern and is not persisted here.
// Đây là module Tier-1 (Non-Critical): Lỗi khởi tạo phân hệ này không được phép
// gây sập hệ thống (Crash-Loopback) mà chỉ làm suy giảm tính năng (Graceful Degradation).
type HypervisorModule struct {
	enabled         bool
	err             error // Lưu vết lỗi khởi tạo để phục vụ Observability (SRE monitor)
	db              *pgxpool.Pool
	cfg             *config.Config
	L1Registry      *cacheengine.CacheRegistry
	VMRepository    hypervisorRepoInterface.PersonalVMRepository
	VMService       hypervisorSvcInterface.PersonalVMService
	VMHandler       *hypervisorHandler.PersonalVMHandler
	ImageRepository hypervisorRepoInterface.ImageRepository
	ImageService    hypervisorSvcInterface.ImageService
	ImageHandler    *hypervisorHandler.ImageHandler

	// Background workflow transports
	CommercialAdmissionProjection *hypervisorStream.CommercialAdmissionProjectionConsumer
	ResourcePlanProjection        *hypervisorStream.ResourcePlanProjectionConsumer
}

// IsEnabled trả về true nếu module được khởi tạo thành công và sẵn sàng phục vụ.
func (m *HypervisorModule) IsEnabled() bool {
	return m != nil && m.enabled
}

// GetError trả về lỗi khởi tạo (nếu có) phục vụ logging và monitoring.
func (m *HypervisorModule) GetError() error {
	if m == nil {
		return errors.New("hypervisor module is nil")
	}
	return m.err
}

// NewModule khởi tạo HypervisorModule với đầy đủ các layered dependencies.
func NewModule(
	cfg *config.Config,
	db *pgxpool.Pool,
	rds *goredis.Client,
	cacheEngine *cacheengine.CacheRegistry,
	otel *observability.OTel,
	protector jobpayload.Protector,
) (*HypervisorModule, error) {
	if cfg == nil {
		return nil, errors.New("hypervisor module: config is required")
	}
	if db == nil {
		return nil, errors.New("hypervisor module: database connection pool is required")
	}
	if rds == nil {
		return nil, errors.New("hypervisor module: Redis client is required")
	}
	if cacheEngine == nil {
		return nil, errors.New("hypervisor module: cache engine registry is required")
	}
	if otel == nil {
		return nil, errors.New("hypervisor module: observability is required")
	}
	if protector == nil {
		return nil, errors.New("hypervisor module: job payload protector is required")
	}
	if strings.TrimSpace(cfg.SchemaSQL.Hypervisor) == "" {
		return nil, errors.New("hypervisor module: SQL schema is empty")
	}

	// Constructors below only wire dependencies with fail-fast nil checks.
	workflowMetrics := otel.WorkflowRecorder("hypervisor")
	vmRepo := hypervisorRepoImpl.NewPersonalVMRepo(db, cfg, protector)
	if vmRepo == nil {
		return nil, errors.New("hypervisor module: failed to construct personal VM repository")
	}
	commercialAdmissionProjectionRepo := hypervisorRepoImpl.NewHypervisorCommercialAdmissionProjectionRepo(db, cfg.SchemaSQL.Hypervisor)
	if commercialAdmissionProjectionRepo == nil {
		return nil, errors.New("hypervisor module: failed to construct commercial admission projection repository")
	}
	commercialAdmissionProjectionSvc := hypervisorSvcImpl.NewHypervisorCommercialAdmissionProjectionService(commercialAdmissionProjectionRepo)
	if commercialAdmissionProjectionSvc == nil {
		return nil, errors.New("hypervisor module: failed to construct commercial admission projection service")
	}
	commercialAdmissionProjection := hypervisorStream.NewCommercialAdmissionProjectionConsumer(rds, commercialAdmissionProjectionSvc)
	if commercialAdmissionProjection == nil {
		return nil, errors.New("hypervisor module: failed to construct commercial admission projection consumer")
	}
	resourcePlanProjectionRepo := hypervisorRepoImpl.NewHypervisorResourcePlanProjectionRepository(db, cfg)
	if resourcePlanProjectionRepo == nil {
		return nil, errors.New("hypervisor module: failed to construct resource plan projection repository")
	}
	resourcePlanProjectionSvc := hypervisorSvcImpl.NewHypervisorResourcePlanProjectionService(resourcePlanProjectionRepo, rds)
	if resourcePlanProjectionSvc == nil {
		return nil, errors.New("hypervisor module: failed to construct resource plan projection service")
	}
	resourcePlanProjection := hypervisorStream.NewResourcePlanProjectionConsumer(rds, resourcePlanProjectionSvc)
	if resourcePlanProjection == nil {
		return nil, errors.New("hypervisor module: failed to construct resource plan projection consumer")
	}
	vmSvc := hypervisorSvcImpl.NewPersonalVMService(vmRepo, rds, workflowMetrics)
	if vmSvc == nil {
		return nil, errors.New("hypervisor module: failed to construct personal VM service")
	}
	vmHandler := hypervisorHandler.NewPersonalVMHandler(vmSvc)
	if vmHandler == nil {
		return nil, errors.New("hypervisor module: failed to construct personal VM handler")
	}
	imageRepo := hypervisorRepoImpl.NewImageRepo(db, cfg, protector)
	if imageRepo == nil {
		return nil, errors.New("hypervisor module: failed to construct image repository")
	}
	imageSvc := hypervisorSvcImpl.NewImageService(imageRepo, workflowMetrics)
	if imageSvc == nil {
		return nil, errors.New("hypervisor module: failed to construct image service")
	}
	imageHandler := hypervisorHandler.NewImageHandler(imageSvc)
	if imageHandler == nil {
		return nil, errors.New("hypervisor module: failed to construct image handler")
	}

	return &HypervisorModule{
		enabled:                       true,
		db:                            db,
		cfg:                           cfg,
		L1Registry:                    cacheEngine,
		VMRepository:                  vmRepo,
		VMService:                     vmSvc,
		VMHandler:                     vmHandler,
		CommercialAdmissionProjection: commercialAdmissionProjection,
		ResourcePlanProjection:        resourcePlanProjection,
		ImageRepository:               imageRepo,
		ImageService:                  imageSvc,
		ImageHandler:                  imageHandler,
	}, nil
}

// NewDegradedModule tạo một phân hệ ảo hóa câm (Dummy/Disabled) mang theo thông điệp lỗi.
// Giúp tránh việc gọi các API gây nil pointer panic và lưu vết lỗi chẩn đoán (Diagnostic).
func NewDegradedModule(err error) *HypervisorModule {
	return &HypervisorModule{
		enabled: false,
		err:     err,
	}
}

// Bootstrap khởi chạy các scheduler ngầm của hypervisor module.
func (m *HypervisorModule) Bootstrap(ctx context.Context) error {
	if !m.IsEnabled() {
		// Bỏ qua không khởi chạy các scheduler ngầm nếu module bị degraded
		return nil
	}
	if m.CommercialAdmissionProjection != nil {
		if err := m.CommercialAdmissionProjection.Start(); err != nil {
			return err
		}
	}
	if m.ResourcePlanProjection != nil {
		if err := m.ResourcePlanProjection.Start(); err != nil {
			if m.CommercialAdmissionProjection != nil {
				m.CommercialAdmissionProjection.Stop()
			}
			return err
		}
	}
	return nil
}

func (m *HypervisorModule) Stop() {
	if !m.IsEnabled() {
		return
	}
	if m.CommercialAdmissionProjection != nil {
		m.CommercialAdmissionProjection.Stop()
	}
	if m.ResourcePlanProjection != nil {
		m.ResourcePlanProjection.Stop()
	}
}
