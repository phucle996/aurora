// Mail module chỉ wire bốn luồng rõ ràng:
// Personal/Tenant × Consumer/Template. PostgreSQL giữ business data; Cache Redis chỉ giữ runtime
// watch soft state. CDC/job-proxy đọc outbox bằng logical replication và không poll DB trong process này.

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
func NewModule(cfg *config.Config, db *pgxpool.Pool, runtimeRedis *goredis.Client, cacheEngine *cacheengine.CacheRegistry) (*Module, error) {
	if cfg == nil || db == nil || runtimeRedis == nil || cacheEngine == nil {
		return nil, errors.New("mail module: config, postgres pool, runtime redis, and cache registry are required")
	}

	// [COMMENT]: Repository được tách ngay tại DI boundary; không có generic repo tự chọn scope lúc runtime.
	personalConsumerRepo := mailRepoImpl.NewPersonalConsumerRepository(db, cfg)
	tenantConsumerRepo := mailRepoImpl.NewTenantConsumerRepository(db, cfg)
	personalTemplateRepo := mailRepoImpl.NewPersonalTemplateRepository(db, cfg)
	tenantTemplateRepo := mailRepoImpl.NewTenantTemplateRepository(db, cfg)
	if personalConsumerRepo == nil || tenantConsumerRepo == nil || personalTemplateRepo == nil || tenantTemplateRepo == nil {
		return nil, errors.New("mail module: failed to construct scoped repositories")
	}

	// [COMMENT]: Cache Redis chỉ giữ watch lease và runtime snapshot có TTL; cấu hình business
	// vẫn nằm trong PostgreSQL và runtime động không đi qua Zone NATS KV.
	personalConsumerSvc := mailSvcImpl.NewPersonalConsumerService(personalConsumerRepo, runtimeRedis)
	tenantConsumerSvc := mailSvcImpl.NewTenantConsumerService(tenantConsumerRepo, runtimeRedis)
	personalTemplateSvc := mailSvcImpl.NewPersonalTemplateService(personalTemplateRepo)
	tenantTemplateSvc := mailSvcImpl.NewTenantTemplateService(tenantTemplateRepo)
	personalConsumerHandler := mailHandler.NewPersonalConsumerHandler(personalConsumerSvc)
	tenantConsumerHandler := mailHandler.NewTenantConsumerHandler(tenantConsumerSvc)
	personalTemplateHandler := mailHandler.NewPersonalTemplateHandler(personalTemplateSvc)
	tenantTemplateHandler := mailHandler.NewTenantTemplateHandler(tenantTemplateSvc)

	return &Module{
		enabled:                 true,
		L1Registry:              cacheEngine,
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
