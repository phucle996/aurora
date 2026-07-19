// Mail module chỉ wire bốn luồng rõ ràng:
// Personal/Tenant × Consumer/Template. PostgreSQL là dependency runtime duy nhất của Phase 2-3;
// CDC/job-proxy đọc outbox bằng logical replication và không chạy poller trong process này.

package mail

import (
	"context"
	"controlplane/internal/config"
	mailRepoInterface "controlplane/internal/mail/domain/repo"
	mailSvcInterface "controlplane/internal/mail/domain/service"
	mailRepoImpl "controlplane/internal/mail/repository"
	mailSvcImpl "controlplane/internal/mail/service"
	mailHandler "controlplane/internal/mail/transport/http/handler"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Module struct {
	enabled bool
	err     error

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

// NewModule constructs the Dependency Graph for the Mail Module.
// coreModule is required to resolve cross-module dependencies (e.g. ZoneService for endpoint zone resolution).
func NewModule(cfg *config.Config, db *pgxpool.Pool) (*Module, error) {
	if cfg == nil || db == nil {
		return nil, errors.New("mail module: config and postgres pool are required")
	}

	// [COMMENT]: Repository được tách ngay tại DI boundary; không có generic repo tự chọn scope lúc runtime.
	personalConsumerRepo := mailRepoImpl.NewPersonalConsumerRepository(db, cfg)
	tenantConsumerRepo := mailRepoImpl.NewTenantConsumerRepository(db, cfg)
	personalTemplateRepo := mailRepoImpl.NewPersonalTemplateRepository(db, cfg)
	tenantTemplateRepo := mailRepoImpl.NewTenantTemplateRepository(db, cfg)
	if personalConsumerRepo == nil || tenantConsumerRepo == nil || personalTemplateRepo == nil || tenantTemplateRepo == nil {
		return nil, errors.New("mail module: failed to construct scoped repositories")
	}

	personalConsumerSvc := mailSvcImpl.NewPersonalConsumerService(personalConsumerRepo)
	tenantConsumerSvc := mailSvcImpl.NewTenantConsumerService(tenantConsumerRepo)
	personalTemplateSvc := mailSvcImpl.NewPersonalTemplateService(personalTemplateRepo)
	tenantTemplateSvc := mailSvcImpl.NewTenantTemplateService(tenantTemplateRepo)
	personalConsumerHandler := mailHandler.NewPersonalConsumerHandler(personalConsumerSvc)
	tenantConsumerHandler := mailHandler.NewTenantConsumerHandler(tenantConsumerSvc)
	personalTemplateHandler := mailHandler.NewPersonalTemplateHandler(personalTemplateSvc)
	tenantTemplateHandler := mailHandler.NewTenantTemplateHandler(tenantTemplateSvc)

	return &Module{
		enabled:                 true,
		PersonalConsumerRepo:    personalConsumerRepo,
		TenantConsumerRepo:      tenantConsumerRepo,
		PersonalTemplateRepo:    personalTemplateRepo,
		TenantTemplateRepo:      tenantTemplateRepo,
		PersonalConsumerService: personalConsumerSvc,
		TenantConsumerService:   tenantConsumerSvc,
		PersonalTemplateService: personalTemplateSvc,
		TenantTemplateService:   tenantTemplateSvc,
		PersonalConsumerHandler: personalConsumerHandler,
		TenantConsumerHandler:   tenantConsumerHandler,
		PersonalTemplateHandler: personalTemplateHandler,
		TenantTemplateHandler:   tenantTemplateHandler,
	}, nil
}

// Start quản lý vòng đời của các tiến trình chạy ngầm (đã lược bỏ OutboxPoller).
func (m *Module) Start(ctx context.Context) error {
	return nil
}

// Stop dừng dọn dẹp các tiến trình chạy ngầm khi tắt control plane gracefully.
func (m *Module) Stop(ctx context.Context) error {
	return nil
}
