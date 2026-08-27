// Mail module chỉ wire bốn luồng rõ ràng:
// Personal/Tenant × Consumer/Template. PostgreSQL giữ business data; runtime read thuộc Zone
// OTel/Victoria. CDC/job-proxy đọc outbox bằng logical replication và không poll DB trong process này.

package mail

import (
	"context"
	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	mailRepoInterface "controlplane/internal/mail/domain/repo"
	mailSvcInterface "controlplane/internal/mail/domain/service"
	mailRepoImpl "controlplane/internal/mail/repository"
	mailSvcImpl "controlplane/internal/mail/service"
	mailHandler "controlplane/internal/mail/transport/http/handler"
	mailStream "controlplane/internal/mail/transport/stream"
	"controlplane/internal/observability"
	jobpayload "controlplane/internal/security"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

type Module struct {
	enabled bool
	err     error
	// [COMMENT]: Mail routes dùng chung RBAC L1/L2 registry; repository ownership guard vẫn là lớp bảo vệ thứ hai.
	L1Registry *cacheengine.CacheRegistry

	// 1) Repositories
	PersonalConsumerRepo mailRepoInterface.PersonalConsumerRepository
	TenantConsumerRepo   mailRepoInterface.TenantConsumerRepository
	PersonalTemplateRepo mailRepoInterface.PersonalTemplateRepository
	TenantTemplateRepo   mailRepoInterface.TenantTemplateRepository

	// 2) Services
	PersonalConsumerService mailSvcInterface.PersonalConsumerService
	TenantConsumerService   mailSvcInterface.TenantConsumerService
	PersonalTemplateService mailSvcInterface.PersonalTemplateService
	TenantTemplateService   mailSvcInterface.TenantTemplateService

	// 3) Handlers
	PersonalConsumerHandler *mailHandler.PersonalConsumerHandler
	TenantConsumerHandler   *mailHandler.TenantConsumerHandler
	PersonalTemplateHandler *mailHandler.PersonalTemplateHandler
	TenantTemplateHandler   *mailHandler.TenantTemplateHandler

	CommercialAdmissionProjection *mailStream.CommercialAdmissionProjectionConsumer
}

// IsEnabled returns true if the module was successfully initialized and is ready to serve.
func (m *Module) IsEnabled() bool {
	return m != nil && m.enabled
}

// GetError returns the initialization error if any, supporting telemetry and SRE auditing.
func (m *Module) GetError() error {
	if m == nil {
		return errors.New("mail module is nil")
	}
	return m.err
}

// NewDegradedModule constructs a muted/degraded Mail module instance carrying the failure context.
// Prevents downstream nil-pointer crashes and permits selective graceful degradation.
func NewDegradedModule(err error) *Module {
	return &Module{
		enabled: false,
		err:     err,
	}
}

// NewModule constructs the dependency graph for the Mail module.
// CacheRegistry is shared with IAM so route authorization follows the same L1/L2 permission snapshot.
func NewModule(cfg *config.Config, db *pgxpool.Pool, runtimeRedis *goredis.Client, cacheEngine *cacheengine.CacheRegistry, otel *observability.OTel, protector jobpayload.Protector) (*Module, error) {
	if cfg == nil || db == nil || runtimeRedis == nil || cacheEngine == nil || otel == nil || protector == nil {
		return nil, errors.New("mail module: config, postgres pool, runtime redis, cache registry, and observability are required")
	}

	// [COMMENT]: Repository được tách ngay tại DI boundary; không có generic repo tự chọn scope lúc runtime.
	personalConsumerRepo := mailRepoImpl.NewPersonalConsumerRepository(db, cfg, protector)
	if personalConsumerRepo == nil {
		return nil, errors.New("mail module: failed to construct personal consumer repository")
	}
	tenantConsumerRepo := mailRepoImpl.NewTenantConsumerRepository(db, cfg, protector)
	if tenantConsumerRepo == nil {
		return nil, errors.New("mail module: failed to construct tenant consumer repository")
	}
	personalTemplateRepo := mailRepoImpl.NewPersonalTemplateRepository(db, cfg, protector)
	if personalTemplateRepo == nil {
		return nil, errors.New("mail module: failed to construct personal template repository")
	}
	tenantTemplateRepo := mailRepoImpl.NewTenantTemplateRepository(db, cfg, protector)
	if tenantTemplateRepo == nil {
		return nil, errors.New("mail module: failed to construct tenant template repository")
	}
	commercialAdmissionProjectionRepo := mailRepoImpl.NewMailCommercialAdmissionProjectionRepo(db, cfg.SchemaSQL.Mail)
	if commercialAdmissionProjectionRepo == nil {
		return nil, errors.New("mail module: failed to construct commercial admission projection repository")
	}
	commercialAdmissionProjectionSvc := mailSvcImpl.NewMailCommercialAdmissionProjectionService(commercialAdmissionProjectionRepo)
	if commercialAdmissionProjectionSvc == nil {
		return nil, errors.New("mail module: failed to construct commercial admission projection service")
	}
	commercialAdmissionProjection := mailStream.NewCommercialAdmissionProjectionConsumer(runtimeRedis, commercialAdmissionProjectionSvc)
	if commercialAdmissionProjection == nil {
		return nil, errors.New("mail module: failed to construct commercial admission projection consumer")
	}

	workflowMetrics := otel.WorkflowRecorder("mail")
	personalConsumerSvc := mailSvcImpl.NewPersonalConsumerService(personalConsumerRepo, workflowMetrics)
	if personalConsumerSvc == nil {
		return nil, errors.New("mail module: failed to construct personal consumer service")
	}
	tenantConsumerSvc := mailSvcImpl.NewTenantConsumerService(tenantConsumerRepo, workflowMetrics)
	if tenantConsumerSvc == nil {
		return nil, errors.New("mail module: failed to construct tenant consumer service")
	}
	personalTemplateSvc := mailSvcImpl.NewPersonalTemplateService(personalTemplateRepo, workflowMetrics)
	if personalTemplateSvc == nil {
		return nil, errors.New("mail module: failed to construct personal template service")
	}
	tenantTemplateSvc := mailSvcImpl.NewTenantTemplateService(tenantTemplateRepo, workflowMetrics)
	if tenantTemplateSvc == nil {
		return nil, errors.New("mail module: failed to construct tenant template service")
	}
	personalConsumerHandler := mailHandler.NewPersonalConsumerHandler(personalConsumerSvc)
	if personalConsumerHandler == nil {
		return nil, errors.New("mail module: failed to construct personal consumer handler")
	}
	tenantConsumerHandler := mailHandler.NewTenantConsumerHandler(tenantConsumerSvc)
	if tenantConsumerHandler == nil {
		return nil, errors.New("mail module: failed to construct tenant consumer handler")
	}
	personalTemplateHandler := mailHandler.NewPersonalTemplateHandler(personalTemplateSvc)
	if personalTemplateHandler == nil {
		return nil, errors.New("mail module: failed to construct personal template handler")
	}
	tenantTemplateHandler := mailHandler.NewTenantTemplateHandler(tenantTemplateSvc)
	if tenantTemplateHandler == nil {
		return nil, errors.New("mail module: failed to construct tenant template handler")
	}

	return &Module{
		enabled:                       true,
		L1Registry:                    cacheEngine,
		PersonalConsumerRepo:          personalConsumerRepo,
		TenantConsumerRepo:            tenantConsumerRepo,
		PersonalTemplateRepo:          personalTemplateRepo,
		TenantTemplateRepo:            tenantTemplateRepo,
		PersonalConsumerService:       personalConsumerSvc,
		TenantConsumerService:         tenantConsumerSvc,
		PersonalTemplateService:       personalTemplateSvc,
		TenantTemplateService:         tenantTemplateSvc,
		PersonalConsumerHandler:       personalConsumerHandler,
		TenantConsumerHandler:         tenantConsumerHandler,
		PersonalTemplateHandler:       personalTemplateHandler,
		TenantTemplateHandler:         tenantTemplateHandler,
		CommercialAdmissionProjection: commercialAdmissionProjection,
	}, nil
}

// Start quản lý vòng đời của các tiến trình chạy ngầm (đã lược bỏ OutboxPoller).
func (m *Module) Start(ctx context.Context) error {
	if m.IsEnabled() && m.CommercialAdmissionProjection != nil {
		if err := m.CommercialAdmissionProjection.Start(); err != nil {
			return err
		}
	}
	return nil
}

// Stop dừng dọn dẹp các tiến trình chạy ngầm khi tắt control plane gracefully.
func (m *Module) Stop(ctx context.Context) error {
	if m.IsEnabled() && m.CommercialAdmissionProjection != nil {
		m.CommercialAdmissionProjection.Stop()
	}
	return nil
}
