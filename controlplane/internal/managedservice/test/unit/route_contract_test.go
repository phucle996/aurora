package unit

import (
	"testing"

	"controlplane/internal/managedservice"
	"controlplane/internal/managedservice/transport/http/handler"

	"github.com/gin-gonic/gin"
)

func TestManagedServiceRoutesKeepRuntimeMutationsBehindCriticalPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	module := &managedservice.Module{
		CategoryHandler:               handler.NewCategoryHandler(nil),
		DefinitionHandler:             handler.NewDefinitionHandler(nil),
		VersionHandler:                handler.NewVersionHandler(nil),
		BlueprintHandler:              handler.NewBlueprintHandler(nil),
		RevisionHandler:               handler.NewRevisionHandler(nil),
		AuditHandler:                  handler.NewAuditHandler(nil),
		PersonalCatalogHandler:        handler.NewPersonalCatalogHandler(nil),
		PersonalCatalogVersionHandler: handler.NewPersonalCatalogVersionHandler(nil),
		TenantCatalogHandler:          handler.NewTenantCatalogHandler(nil),
		TenantCatalogVersionHandler:   handler.NewTenantCatalogVersionHandler(nil),
	}
	router := gin.New()
	managedservice.RegisterRoutes(router, module)

	routes := make(map[string]struct{})
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}
	for _, route := range []string{
		"POST /admin/critical/managed-services/catalog/versions/:version_id/blueprints",
		"POST /admin/critical/managed-services/catalog/blueprints/:blueprint_id/drafts",
		"PATCH /admin/critical/managed-services/catalog/drafts/:draft_id",
		"POST /admin/critical/managed-services/catalog/drafts/:draft_id/validate",
		"POST /admin/critical/managed-services/catalog/drafts/:draft_id/publish",
		"POST /admin/critical/managed-services/catalog/revisions/:revision_id/retire",
		"DELETE /admin/critical/managed-services/catalog/drafts/:draft_id",
	} {
		if _, exists := routes[route]; !exists {
			t.Fatalf("missing critical route %q", route)
		}
	}
	for _, route := range []string{
		"GET /api/v1/personal/managed-services/catalog",
		"GET /api/v1/personal/managed-services/catalog/versions/:version_id",
		"GET /api/v1/tenant/managed-services/catalog",
		"GET /api/v1/tenant/managed-services/catalog/versions/:version_id",
	} {
		if _, exists := routes[route]; !exists {
			t.Fatalf("missing customer catalog route %q", route)
		}
	}
	for route := range routes {
		if route == "POST /admin/managed-services/catalog/blueprints" ||
			route == "POST /admin/managed-services/catalog/drafts" ||
			route == "POST /admin/managed-services/catalog/publish" {
			t.Fatalf("runtime-affecting mutation escaped critical path: %s", route)
		}
	}
}
