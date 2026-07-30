package hypervisor

import (
	"context"
	"errors"

	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	hypervisorRepoInterface "controlplane/internal/hypervisor/domain/repo"
	hypervisorSvcInterface "controlplane/internal/hypervisor/domain/service"
	hypervisorRepoImpl "controlplane/internal/hypervisor/repository"
	hypervisorSvcImpl "controlplane/internal/hypervisor/service"
	hypervisorHandler "controlplane/internal/hypervisor/transport/http/handler"
	"controlplane/internal/observability"

	"github.com/jackc/pgx/v5/pgxpool"
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
	cacheEngine *cacheengine.CacheRegistry,
	otel *observability.OTel,
) (*HypervisorModule, error) {
	if cfg == nil {
		return nil, errors.New("hypervisor module: config is required")
	}
	if db == nil {
		return nil, errors.New("hypervisor module: database connection pool is required")
	}
	if cacheEngine == nil {
		return nil, errors.New("hypervisor module: cache engine registry is required")
	}
	if otel == nil {
		return nil, errors.New("hypervisor module: observability is required")
	}

	// Constructors below only wire dependencies; request validation remains at
	// the HTTP handler and is not repeated in service or repository layers.
	workflowMetrics := otel.WorkflowRecorder("hypervisor")
	vmRepo := hypervisorRepoImpl.NewPersonalVMRepo(db, cfg)
	vmSvc := hypervisorSvcImpl.NewPersonalVMService(vmRepo, workflowMetrics)
	vmHandler := hypervisorHandler.NewPersonalVMHandler(vmSvc)
	imageRepo := hypervisorRepoImpl.NewImageRepo(db, cfg)
	imageSvc := hypervisorSvcImpl.NewImageService(imageRepo, workflowMetrics)
	imageHandler := hypervisorHandler.NewImageHandler(imageSvc)

	return &HypervisorModule{
		enabled:         true,
		db:              db,
		cfg:             cfg,
		L1Registry:      cacheEngine,
		VMRepository:    vmRepo,
		VMService:       vmSvc,
		VMHandler:       vmHandler,
		ImageRepository: imageRepo,
		ImageService:    imageSvc,
		ImageHandler:    imageHandler,
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
	// VM execution and node observation run outside Controlplane.
	return nil
}

func (m *HypervisorModule) Stop() {
	if !m.IsEnabled() {
		return
	}
	// Dừng an toàn các workers
}
