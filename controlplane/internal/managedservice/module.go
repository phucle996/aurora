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

	"github.com/jackc/pgx/v5/pgxpool"
)

type Module struct {
	L1Registry *cacheengine.CacheRegistry

	CategoryRepository               managedrepo.CategoryRepository
	DefinitionRepository             managedrepo.DefinitionRepository
	VersionRepository                managedrepo.VersionRepository
	BlueprintRepository              managedrepo.BlueprintRepository
	RevisionRepository               managedrepo.RevisionRepository
	AuditRepository                  managedrepo.AuditRepository
	PersonalCatalogRepository        managedrepo.PersonalCatalogRepository
	PersonalCatalogVersionRepository managedrepo.PersonalCatalogVersionRepository
	TenantCatalogRepository          managedrepo.TenantCatalogRepository
	TenantCatalogVersionRepository   managedrepo.TenantCatalogVersionRepository

	CategoryService               managedservice.CategoryService
	DefinitionService             managedservice.DefinitionService
	VersionService                managedservice.VersionService
	BlueprintService              managedservice.BlueprintService
	RevisionService               managedservice.RevisionService
	AuditService                  managedservice.AuditService
	PersonalCatalogService        managedservice.PersonalCatalogService
	PersonalCatalogVersionService managedservice.PersonalCatalogVersionService
	TenantCatalogService          managedservice.TenantCatalogService
	TenantCatalogVersionService   managedservice.TenantCatalogVersionService

	CategoryHandler               *handler.CategoryHandler
	DefinitionHandler             *handler.DefinitionHandler
	VersionHandler                *handler.VersionHandler
	BlueprintHandler              *handler.BlueprintHandler
	RevisionHandler               *handler.RevisionHandler
	AuditHandler                  *handler.AuditHandler
	PersonalCatalogHandler        *handler.PersonalCatalogHandler
	PersonalCatalogVersionHandler *handler.PersonalCatalogVersionHandler
	TenantCatalogHandler          *handler.TenantCatalogHandler
	TenantCatalogVersionHandler   *handler.TenantCatalogVersionHandler
}

func NewModule(cfg *config.Config, db *pgxpool.Pool, cacheEngine *cacheengine.CacheRegistry) (*Module, error) {
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
	tenantCatalogRepository := repositoryimpl.NewTenantCatalogRepository(db, schema, hierarchySchema)
	tenantCatalogVersionRepository := repositoryimpl.NewTenantCatalogVersionRepository(db, schema, hierarchySchema)

	categoryService := serviceimpl.NewCategoryService(categoryRepository)
	definitionService := serviceimpl.NewDefinitionService(definitionRepository)
	versionService := serviceimpl.NewVersionService(versionRepository)
	blueprintService := serviceimpl.NewBlueprintService(blueprintRepository)
	revisionService := serviceimpl.NewRevisionService(revisionRepository)
	auditService := serviceimpl.NewAuditService(auditRepository)
	personalCatalogService := serviceimpl.NewPersonalCatalogService(personalCatalogRepository)
	personalCatalogVersionService := serviceimpl.NewPersonalCatalogVersionService(personalCatalogVersionRepository)
	tenantCatalogService := serviceimpl.NewTenantCatalogService(tenantCatalogRepository)
	tenantCatalogVersionService := serviceimpl.NewTenantCatalogVersionService(tenantCatalogVersionRepository)

	return &Module{
		L1Registry:         cacheEngine,
		CategoryRepository: categoryRepository, DefinitionRepository: definitionRepository,
		VersionRepository: versionRepository, BlueprintRepository: blueprintRepository,
		RevisionRepository: revisionRepository, AuditRepository: auditRepository,
		PersonalCatalogRepository:        personalCatalogRepository,
		PersonalCatalogVersionRepository: personalCatalogVersionRepository,
		TenantCatalogRepository:          tenantCatalogRepository,
		TenantCatalogVersionRepository:   tenantCatalogVersionRepository,
		CategoryService:                  categoryService, DefinitionService: definitionService,
		VersionService: versionService, BlueprintService: blueprintService,
		RevisionService: revisionService, AuditService: auditService,
		PersonalCatalogService:        personalCatalogService,
		PersonalCatalogVersionService: personalCatalogVersionService,
		TenantCatalogService:          tenantCatalogService,
		TenantCatalogVersionService:   tenantCatalogVersionService,
		CategoryHandler:               handler.NewCategoryHandler(categoryService),
		DefinitionHandler:             handler.NewDefinitionHandler(definitionService),
		VersionHandler:                handler.NewVersionHandler(versionService),
		BlueprintHandler:              handler.NewBlueprintHandler(blueprintService),
		RevisionHandler:               handler.NewRevisionHandler(revisionService),
		AuditHandler:                  handler.NewAuditHandler(auditService),
		PersonalCatalogHandler:        handler.NewPersonalCatalogHandler(personalCatalogService),
		PersonalCatalogVersionHandler: handler.NewPersonalCatalogVersionHandler(personalCatalogVersionService),
		TenantCatalogHandler:          handler.NewTenantCatalogHandler(tenantCatalogService),
		TenantCatalogVersionHandler:   handler.NewTenantCatalogVersionHandler(tenantCatalogVersionService),
	}, nil
}
