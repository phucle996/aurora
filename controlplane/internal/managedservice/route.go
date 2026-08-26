package managedservice

import (
	"controlplane/internal/http/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(router *gin.Engine, module *Module) {
	router.POST("/admin/managed-services/catalog/categories", module.CategoryHandler.CreateCategory)
	router.GET("/admin/managed-services/catalog/categories", module.CategoryHandler.ListCategories)
	router.GET("/admin/managed-services/catalog/categories/:category_id", module.CategoryHandler.GetCategory)
	router.PATCH("/admin/managed-services/catalog/categories/:category_id", module.CategoryHandler.UpdateCategory)
	router.POST("/admin/managed-services/catalog/definitions", module.DefinitionHandler.CreateDefinition)
	router.GET("/admin/managed-services/catalog/definitions", module.DefinitionHandler.ListDefinitions)
	router.GET("/admin/managed-services/catalog/definitions/:definition_id", module.DefinitionHandler.GetDefinition)
	router.PATCH("/admin/managed-services/catalog/definitions/:definition_id", module.DefinitionHandler.UpdateDefinition)
	router.POST("/admin/managed-services/catalog/versions", module.VersionHandler.CreateVersion)
	router.GET("/admin/managed-services/catalog/versions", module.VersionHandler.ListVersions)
	router.GET("/admin/managed-services/catalog/versions/:version_id", module.VersionHandler.GetVersion)
	router.PATCH("/admin/managed-services/catalog/versions/:version_id", module.VersionHandler.UpdateVersion)
	router.GET("/admin/managed-services/catalog/versions/:version_id/blueprint", module.BlueprintHandler.GetBlueprintByVersion)
	router.GET("/admin/managed-services/catalog/blueprints/:blueprint_id", module.BlueprintHandler.GetBlueprint)
	router.GET("/admin/managed-services/catalog/blueprints/:blueprint_id/revisions", module.RevisionHandler.ListRevisions)
	router.GET("/admin/managed-services/catalog/drafts/:draft_id", module.RevisionHandler.GetDraft)
	router.GET("/admin/managed-services/catalog/audit", module.AuditHandler.ListAuditEvents)

	// [COMMENT]: Every mutation capable of changing the published runtime
	// contract is structurally routed through ACR's /admin/critical policy.
	router.POST("/admin/critical/managed-services/catalog/categories/:category_id/retire", module.CategoryHandler.RetireCategory)
	router.POST("/admin/critical/managed-services/catalog/definitions/:definition_id/retire", module.DefinitionHandler.RetireDefinition)
	router.POST("/admin/critical/managed-services/catalog/versions/:version_id/deprecate", module.VersionHandler.DeprecateVersion)
	router.POST("/admin/critical/managed-services/catalog/versions/:version_id/retire", module.VersionHandler.RetireVersion)
	router.POST("/admin/critical/managed-services/catalog/versions/:version_id/blueprints", module.BlueprintHandler.CreateBlueprint)
	router.DELETE("/admin/critical/managed-services/catalog/blueprints/:blueprint_id", module.BlueprintHandler.DeleteBlueprint)
	router.POST("/admin/critical/managed-services/catalog/blueprints/:blueprint_id/drafts", module.RevisionHandler.CreateDraft)
	router.PATCH("/admin/critical/managed-services/catalog/drafts/:draft_id", module.RevisionHandler.PatchDraft)
	router.POST("/admin/critical/managed-services/catalog/drafts/:draft_id/validate", module.RevisionHandler.ValidateDraft)
	router.POST("/admin/critical/managed-services/catalog/drafts/:draft_id/publish", module.RevisionHandler.PublishDraft)
	router.POST("/admin/critical/managed-services/catalog/revisions/:revision_id/retire", module.RevisionHandler.RetireRevision)
	router.DELETE("/admin/critical/managed-services/catalog/drafts/:draft_id", module.RevisionHandler.DeleteDraft)

	router.GET("/api/v1/personal/managed-services/catalog",
		middleware.Authorize("managed-service:catalog:read", module.L1Registry, "*"),
		module.PersonalCatalogHandler.ListPersonalCatalog,
	)
	router.GET("/api/v1/personal/managed-services/catalog/versions/:version_id",
		middleware.Authorize("managed-service:catalog:read", module.L1Registry, "*"),
		module.PersonalCatalogVersionHandler.GetPersonalCatalogVersion,
	)
	router.GET("/api/v1/personal/managed-services/instances",
		middleware.Authorize("managed-service:instance:read", module.L1Registry, "*"),
		module.PersonalInstanceHandler.ListPersonalInstances,
	)
	router.GET("/api/v1/personal/managed-services/instances/:code",
		middleware.Authorize("managed-service:instance:read", module.L1Registry, "*"),
		module.PersonalInstanceHandler.GetPersonalInstance,
	)
	router.GET("/api/v1/personal/managed-services/instances/:code/operations",
		middleware.Authorize("managed-service:instance:read", module.L1Registry, "*"),
		module.PersonalInstanceHandler.ListPersonalInstanceOperations,
	)
	router.GET("/api/v1/personal/managed-services/instances/:code/operations/:operation_id",
		middleware.Authorize("managed-service:instance:read", module.L1Registry, "*"),
		module.PersonalInstanceHandler.GetPersonalInstanceOperation,
	)
	router.POST("/api/v1/personal/critical/managed-services/instances",
		middleware.RequireSessionProof(),
		middleware.Authorize("managed-service:instance:write", module.L1Registry, "*"),
		module.PersonalInstanceHandler.CreatePersonalInstance,
	)
	router.PATCH("/api/v1/personal/managed-services/instances/:code/name",
		middleware.Authorize("managed-service:instance:write", module.L1Registry, "*"),
		module.PersonalInstanceHandler.RenamePersonalInstance,
	)
	router.POST("/api/v1/personal/critical/managed-services/instances/:code/resize",
		middleware.RequireSessionProof(),
		middleware.Authorize("managed-service:instance:write", module.L1Registry, "*"),
		module.PersonalInstanceHandler.ResizePersonalInstance,
	)
	router.DELETE("/api/v1/personal/critical/managed-services/instances/:code",
		middleware.RequireSessionProof(),
		middleware.Authorize("managed-service:instance:write", module.L1Registry, "*"),
		module.PersonalInstanceHandler.DeletePersonalInstance,
	)
	router.POST("/api/v1/personal/critical/managed-services/instances/:code/operations/:operation_id/retry",
		middleware.RequireSessionProof(),
		middleware.Authorize("managed-service:instance:write", module.L1Registry, "*"),
		module.PersonalInstanceHandler.RetryPersonalInstance,
	)
	router.GET("/api/v1/tenant/managed-services/catalog",
		middleware.Authorize("managed-service:catalog:read", module.L1Registry, "*"),
		module.TenantCatalogHandler.ListTenantCatalog,
	)
	router.GET("/api/v1/tenant/managed-services/catalog/versions/:version_id",
		middleware.Authorize("managed-service:catalog:read", module.L1Registry, "*"),
		module.TenantCatalogVersionHandler.GetTenantCatalogVersion,
	)
	router.GET("/api/v1/tenant/managed-services/instances",
		middleware.Authorize("managed-service:instance:read", module.L1Registry, "*"),
		module.TenantInstanceHandler.ListTenantInstances,
	)
	router.GET("/api/v1/tenant/managed-services/instances/:code",
		middleware.Authorize("managed-service:instance:read", module.L1Registry, "*"),
		module.TenantInstanceHandler.GetTenantInstance,
	)
	router.GET("/api/v1/tenant/managed-services/instances/:code/operations",
		middleware.Authorize("managed-service:instance:read", module.L1Registry, "*"),
		module.TenantInstanceHandler.ListTenantInstanceOperations,
	)
	router.GET("/api/v1/tenant/managed-services/instances/:code/operations/:operation_id",
		middleware.Authorize("managed-service:instance:read", module.L1Registry, "*"),
		module.TenantInstanceHandler.GetTenantInstanceOperation,
	)
	router.POST("/api/v1/tenant/critical/managed-services/instances",
		middleware.RequireSessionProof(),
		middleware.Authorize("managed-service:instance:write", module.L1Registry, "*"),
		module.TenantInstanceHandler.CreateTenantInstance,
	)
	router.PATCH("/api/v1/tenant/managed-services/instances/:code/name",
		middleware.Authorize("managed-service:instance:write", module.L1Registry, "*"),
		module.TenantInstanceHandler.RenameTenantInstance,
	)
	router.POST("/api/v1/tenant/critical/managed-services/instances/:code/resize",
		middleware.RequireSessionProof(),
		middleware.Authorize("managed-service:instance:write", module.L1Registry, "*"),
		module.TenantInstanceHandler.ResizeTenantInstance,
	)
	router.DELETE("/api/v1/tenant/critical/managed-services/instances/:code",
		middleware.RequireSessionProof(),
		middleware.Authorize("managed-service:instance:write", module.L1Registry, "*"),
		module.TenantInstanceHandler.DeleteTenantInstance,
	)
	router.POST("/api/v1/tenant/critical/managed-services/instances/:code/operations/:operation_id/retry",
		middleware.RequireSessionProof(),
		middleware.Authorize("managed-service:instance:write", module.L1Registry, "*"),
		module.TenantInstanceHandler.RetryTenantInstance,
	)
}
