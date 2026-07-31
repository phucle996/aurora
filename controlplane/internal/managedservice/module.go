package managedservice

import (
	"errors"
	"regexp"
	"strings"

	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	managedrepo "controlplane/internal/managedservice/domain/repo"
	managedservice "controlplane/internal/managedservice/domain/service"
	repositoryimpl "controlplane/internal/managedservice/repository"
	serviceimpl "controlplane/internal/managedservice/service"
	"controlplane/internal/managedservice/transport/http/handler"
	"controlplane/internal/observability"
	jobpayload "controlplane/internal/security"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Module struct {
	L1Registry *cacheengine.CacheRegistry
	// PayloadProtector is module infrastructure for P04 mutations. It stays at
	// the construction boundary; services do not probe dependencies per flow.
	PayloadProtector jobpayload.Protector

	CategoryRepository               managedrepo.CategoryRepository
	DefinitionRepository             managedrepo.DefinitionRepository
	VersionRepository                managedrepo.VersionRepository
	BlueprintRepository              managedrepo.BlueprintRepository
	RevisionRepository               managedrepo.RevisionRepository
	AuditRepository                  managedrepo.AuditRepository
	PersonalCatalogRepository        managedrepo.PersonalCatalogRepository
	PersonalCatalogVersionRepository managedrepo.PersonalCatalogVersionRepository
	PersonalInstanceRepository       managedrepo.PersonalInstanceRepository
	TenantCatalogRepository          managedrepo.TenantCatalogRepository
	TenantCatalogVersionRepository   managedrepo.TenantCatalogVersionRepository
	TenantInstanceRepository         managedrepo.TenantInstanceRepository

	CategoryService               managedservice.CategoryService
	DefinitionService             managedservice.DefinitionService
	VersionService                managedservice.VersionService
	BlueprintService              managedservice.BlueprintService
	RevisionService               managedservice.RevisionService
	AuditService                  managedservice.AuditService
	PersonalCatalogService        managedservice.PersonalCatalogService
	PersonalCatalogVersionService managedservice.PersonalCatalogVersionService
	PersonalInstanceService       managedservice.PersonalInstanceService
	TenantCatalogService          managedservice.TenantCatalogService
	TenantCatalogVersionService   managedservice.TenantCatalogVersionService
	TenantInstanceService         managedservice.TenantInstanceService

	CategoryHandler               *handler.CategoryHandler
	DefinitionHandler             *handler.DefinitionHandler
	VersionHandler                *handler.VersionHandler
	BlueprintHandler              *handler.BlueprintHandler
	RevisionHandler               *handler.RevisionHandler
	AuditHandler                  *handler.AuditHandler
	PersonalCatalogHandler        *handler.PersonalCatalogHandler
	PersonalCatalogVersionHandler *handler.PersonalCatalogVersionHandler
	PersonalInstanceHandler       *handler.PersonalInstanceHandler
	TenantCatalogHandler          *handler.TenantCatalogHandler
	TenantCatalogVersionHandler   *handler.TenantCatalogVersionHandler
	TenantInstanceHandler         *handler.TenantInstanceHandler
}

func NewModule(cfg *config.Config, db *pgxpool.Pool, cacheEngine *cacheengine.CacheRegistry, otel *observability.OTel, payloadProtector jobpayload.Protector) (*Module, error) {
	// [COMMENT]: Dependency health is an app-construction invariant. Workflow
	// services therefore never branch on nil infrastructure at request time.
	if cfg == nil {
		return nil, errors.New("managed service module: config is nil")
	}
	if db == nil {
		return nil, errors.New("managed service module: database pool is nil")
	}
	if cacheEngine == nil {
		return nil, errors.New("managed service module: cache registry is nil")
	}
	if otel == nil {
		return nil, errors.New("managed service module: observability is nil")
	}
	if payloadProtector == nil {
		return nil, errors.New("managed service module: payload protector is nil")
	}
	schema := strings.TrimSpace(cfg.SchemaSQL.ManagedService)
	if !regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`).MatchString(schema) {
		return nil, errors.New("managed service module: database schema is invalid")
	}
	hierarchySchema := strings.TrimSpace(cfg.SchemaSQL.Hierarchy)
	if !regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`).MatchString(hierarchySchema) {
		return nil, errors.New("managed service module: hierarchy database schema is invalid")
	}

	categoryRepository := repositoryimpl.NewCategoryRepository(db, schema)
	definitionRepository := repositoryimpl.NewDefinitionRepository(db, schema)
	versionRepository := repositoryimpl.NewVersionRepository(db, schema)
	blueprintRepository := repositoryimpl.NewBlueprintRepository(db, schema)
	revisionRepository := repositoryimpl.NewRevisionRepository(db, schema)
	auditRepository := repositoryimpl.NewAuditRepository(db, schema)
	personalCatalogRepository := repositoryimpl.NewPersonalCatalogRepository(db, schema, hierarchySchema)
	personalCatalogVersionRepository := repositoryimpl.NewPersonalCatalogVersionRepository(db, schema, hierarchySchema)
	personalInstanceRepository := repositoryimpl.NewPersonalInstanceRepository(db, schema, hierarchySchema, payloadProtector)
	tenantCatalogRepository := repositoryimpl.NewTenantCatalogRepository(db, schema, hierarchySchema)
	tenantCatalogVersionRepository := repositoryimpl.NewTenantCatalogVersionRepository(db, schema, hierarchySchema)
	tenantInstanceRepository := repositoryimpl.NewTenantInstanceRepository(db, schema, hierarchySchema, payloadProtector)

	workflowMetrics := otel.WorkflowRecorder("managedservice")
	categoryService := serviceimpl.NewCategoryService(categoryRepository, workflowMetrics)
	definitionService := serviceimpl.NewDefinitionService(definitionRepository, workflowMetrics)
	versionService := serviceimpl.NewVersionService(versionRepository, workflowMetrics)
	blueprintService := serviceimpl.NewBlueprintService(blueprintRepository, workflowMetrics)
	revisionService := serviceimpl.NewRevisionService(revisionRepository, workflowMetrics)
	auditService := serviceimpl.NewAuditService(auditRepository, workflowMetrics)
	personalCatalogService := serviceimpl.NewPersonalCatalogService(personalCatalogRepository, workflowMetrics)
	personalCatalogVersionService := serviceimpl.NewPersonalCatalogVersionService(personalCatalogVersionRepository, workflowMetrics)
	personalInstanceService := serviceimpl.NewPersonalInstanceService(personalInstanceRepository, workflowMetrics)
	tenantCatalogService := serviceimpl.NewTenantCatalogService(tenantCatalogRepository, workflowMetrics)
	tenantCatalogVersionService := serviceimpl.NewTenantCatalogVersionService(tenantCatalogVersionRepository, workflowMetrics)
	tenantInstanceService := serviceimpl.NewTenantInstanceService(tenantInstanceRepository, workflowMetrics)

	return &Module{
		L1Registry:         cacheEngine,
		PayloadProtector:   payloadProtector,
		CategoryRepository: categoryRepository, DefinitionRepository: definitionRepository,
		VersionRepository: versionRepository, BlueprintRepository: blueprintRepository,
		RevisionRepository: revisionRepository, AuditRepository: auditRepository,
		PersonalCatalogRepository:        personalCatalogRepository,
		PersonalCatalogVersionRepository: personalCatalogVersionRepository,
		PersonalInstanceRepository:       personalInstanceRepository,
		TenantCatalogRepository:          tenantCatalogRepository,
		TenantCatalogVersionRepository:   tenantCatalogVersionRepository,
		TenantInstanceRepository:         tenantInstanceRepository,
		CategoryService:                  categoryService, DefinitionService: definitionService,
		VersionService: versionService, BlueprintService: blueprintService,
		RevisionService: revisionService, AuditService: auditService,
		PersonalCatalogService:        personalCatalogService,
		PersonalCatalogVersionService: personalCatalogVersionService,
		PersonalInstanceService:       personalInstanceService,
		TenantCatalogService:          tenantCatalogService,
		TenantCatalogVersionService:   tenantCatalogVersionService,
		TenantInstanceService:         tenantInstanceService,
		CategoryHandler:               handler.NewCategoryHandler(categoryService),
		DefinitionHandler:             handler.NewDefinitionHandler(definitionService),
		VersionHandler:                handler.NewVersionHandler(versionService),
		BlueprintHandler:              handler.NewBlueprintHandler(blueprintService),
		RevisionHandler:               handler.NewRevisionHandler(revisionService),
		AuditHandler:                  handler.NewAuditHandler(auditService),
		PersonalCatalogHandler:        handler.NewPersonalCatalogHandler(personalCatalogService),
		PersonalCatalogVersionHandler: handler.NewPersonalCatalogVersionHandler(personalCatalogVersionService),
		PersonalInstanceHandler:       handler.NewPersonalInstanceHandler(personalInstanceService),
		TenantCatalogHandler:          handler.NewTenantCatalogHandler(tenantCatalogService),
		TenantCatalogVersionHandler:   handler.NewTenantCatalogVersionHandler(tenantCatalogVersionService),
		TenantInstanceHandler:         handler.NewTenantInstanceHandler(tenantInstanceService),
	}, nil
}
