package managedservice

import (
	"controlplane/internal/http/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(router *gin.Engine, module *Module) {
	admin := router.Group("/admin/managed-services/catalog")
	{
		admin.POST("/categories", module.CategoryHandler.CreateCategory)
		admin.GET("/categories", module.CategoryHandler.ListCategories)
		admin.GET("/categories/:category_id", module.CategoryHandler.GetCategory)
		admin.PATCH("/categories/:category_id", module.CategoryHandler.UpdateCategory)

		admin.POST("/definitions", module.DefinitionHandler.CreateDefinition)
		admin.GET("/definitions", module.DefinitionHandler.ListDefinitions)
		admin.GET("/definitions/:definition_id", module.DefinitionHandler.GetDefinition)
		admin.PATCH("/definitions/:definition_id", module.DefinitionHandler.UpdateDefinition)

		admin.POST("/versions", module.VersionHandler.CreateVersion)
		admin.GET("/versions", module.VersionHandler.ListVersions)
		admin.GET("/versions/:version_id", module.VersionHandler.GetVersion)
		admin.PATCH("/versions/:version_id", module.VersionHandler.UpdateVersion)
		admin.GET("/versions/:version_id/blueprint", module.BlueprintHandler.GetBlueprintByVersion)

		admin.GET("/blueprints/:blueprint_id", module.BlueprintHandler.GetBlueprint)
		admin.GET("/blueprints/:blueprint_id/revisions", module.RevisionHandler.ListRevisions)
		admin.GET("/drafts/:draft_id", module.RevisionHandler.GetDraft)
		admin.GET("/audit", module.AuditHandler.ListAuditEvents)
	}

	// [COMMENT]: Every mutation capable of changing the published runtime
	// contract is structurally routed through ACR's /admin/critical policy.
	critical := router.Group("/admin/critical/managed-services/catalog")
	{
		critical.POST("/categories/:category_id/retire", module.CategoryHandler.RetireCategory)
		critical.POST("/definitions/:definition_id/retire", module.DefinitionHandler.RetireDefinition)
		critical.POST("/versions/:version_id/deprecate", module.VersionHandler.DeprecateVersion)
		critical.POST("/versions/:version_id/retire", module.VersionHandler.RetireVersion)

		critical.POST("/versions/:version_id/blueprints", module.BlueprintHandler.CreateBlueprint)
		critical.DELETE("/blueprints/:blueprint_id", module.BlueprintHandler.DeleteBlueprint)

		critical.POST("/blueprints/:blueprint_id/drafts", module.RevisionHandler.CreateDraft)
		critical.PATCH("/drafts/:draft_id", module.RevisionHandler.PatchDraft)
		critical.POST("/drafts/:draft_id/validate", module.RevisionHandler.ValidateDraft)
		critical.POST("/drafts/:draft_id/publish", module.RevisionHandler.PublishDraft)
		critical.POST("/revisions/:revision_id/retire", module.RevisionHandler.RetireRevision)
		critical.DELETE("/drafts/:draft_id", module.RevisionHandler.DeleteDraft)
	}

	personal := router.Group("/api/v1/personal/managed-services")
	{
		personal.GET("/catalog",
			middleware.Authorize("managed-service:catalog:read", module.L1Registry, "*"),
			module.PersonalCatalogHandler.ListPersonalCatalog,
		)
		personal.GET("/catalog/versions/:version_id",
			middleware.Authorize("managed-service:catalog:read", module.L1Registry, "*"),
			module.PersonalCatalogVersionHandler.GetPersonalCatalogVersion,
		)
		personal.GET("/instances",
			middleware.Authorize("managed-service:instance:read", module.L1Registry, "*"),
			module.PersonalInstanceHandler.ListPersonalInstances,
		)
		personal.GET("/instances/:code",
			middleware.Authorize("managed-service:instance:read", module.L1Registry, "*"),
			module.PersonalInstanceHandler.GetPersonalInstance,
		)
		personal.GET("/instances/:code/operations",
			middleware.Authorize("managed-service:instance:read", module.L1Registry, "*"),
			module.PersonalInstanceHandler.ListPersonalInstanceOperations,
		)
		personal.GET("/instances/:code/operations/:operation_id",
			middleware.Authorize("managed-service:instance:read", module.L1Registry, "*"),
			module.PersonalInstanceHandler.GetPersonalInstanceOperation,
		)
		personal.POST("/instances",
			middleware.Authorize("managed-service:instance:write", module.L1Registry, "*"),
			module.PersonalInstanceHandler.CreatePersonalInstance,
		)
		personal.PATCH("/instances/:code/name",
			middleware.Authorize("managed-service:instance:write", module.L1Registry, "*"),
			module.PersonalInstanceHandler.RenamePersonalInstance,
		)
		personal.POST("/instances/:code/resize",
			middleware.Authorize("managed-service:instance:write", module.L1Registry, "*"),
			module.PersonalInstanceHandler.ResizePersonalInstance,
		)
		personal.DELETE("/instances/:code",
			middleware.Authorize("managed-service:instance:write", module.L1Registry, "*"),
			module.PersonalInstanceHandler.DeletePersonalInstance,
		)
		personal.POST("/instances/:code/operations/:operation_id/retry",
			middleware.Authorize("managed-service:instance:write", module.L1Registry, "*"),
			module.PersonalInstanceHandler.RetryPersonalInstance,
		)
	}

	tenant := router.Group("/api/v1/tenant/managed-services")
	{
		tenant.GET("/catalog",
			middleware.Authorize("managed-service:catalog:read", module.L1Registry, "*"),
			module.TenantCatalogHandler.ListTenantCatalog,
		)
		tenant.GET("/catalog/versions/:version_id",
			middleware.Authorize("managed-service:catalog:read", module.L1Registry, "*"),
			module.TenantCatalogVersionHandler.GetTenantCatalogVersion,
		)
		tenant.GET("/instances",
			middleware.Authorize("managed-service:instance:read", module.L1Registry, "*"),
			module.TenantInstanceHandler.ListTenantInstances,
		)
		tenant.GET("/instances/:code",
			middleware.Authorize("managed-service:instance:read", module.L1Registry, "*"),
			module.TenantInstanceHandler.GetTenantInstance,
		)
		tenant.GET("/instances/:code/operations",
			middleware.Authorize("managed-service:instance:read", module.L1Registry, "*"),
			module.TenantInstanceHandler.ListTenantInstanceOperations,
		)
		tenant.GET("/instances/:code/operations/:operation_id",
			middleware.Authorize("managed-service:instance:read", module.L1Registry, "*"),
			module.TenantInstanceHandler.GetTenantInstanceOperation,
		)
		tenant.POST("/instances",
			middleware.Authorize("managed-service:instance:write", module.L1Registry, "*"),
			module.TenantInstanceHandler.CreateTenantInstance,
		)
		tenant.PATCH("/instances/:code/name",
			middleware.Authorize("managed-service:instance:write", module.L1Registry, "*"),
			module.TenantInstanceHandler.RenameTenantInstance,
		)
		tenant.POST("/instances/:code/resize",
			middleware.Authorize("managed-service:instance:write", module.L1Registry, "*"),
			module.TenantInstanceHandler.ResizeTenantInstance,
		)
		tenant.DELETE("/instances/:code",
			middleware.Authorize("managed-service:instance:write", module.L1Registry, "*"),
			module.TenantInstanceHandler.DeleteTenantInstance,
		)
		tenant.POST("/instances/:code/operations/:operation_id/retry",
			middleware.Authorize("managed-service:instance:write", module.L1Registry, "*"),
			module.TenantInstanceHandler.RetryTenantInstance,
		)
	}
}
