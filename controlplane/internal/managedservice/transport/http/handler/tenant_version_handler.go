package handler

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"controlplane/internal/managedservice/domain/entity"
	managedservice "controlplane/internal/managedservice/domain/service"
	"controlplane/internal/managedservice/taxonomy"
	"controlplane/internal/managedservice/transport/http/dto"
	"controlplane/pkg/apires"
	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type TenantCatalogVersionHandler struct {
	service managedservice.TenantCatalogVersionService
}

func NewTenantCatalogVersionHandler(service managedservice.TenantCatalogVersionService) *TenantCatalogVersionHandler {
	return &TenantCatalogVersionHandler{service: service}
}

func (h *TenantCatalogVersionHandler) GetTenantCatalogVersion(c *gin.Context) {
	const op = "managedservice.tenant_version.get"
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	tenantID, ok := pkgcontext.GetTenantID(c, op)
	if !ok {
		return
	}
	workspaceID, ok := pkgcontext.GetWorkspaceID(c, op)
	if !ok {
		return
	}
	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}
	versionID, err := uuid.Parse(strings.TrimSpace(c.Param("version_id")))
	if err != nil || versionID == uuid.Nil {
		apires.RespondBadRequest(c, "invalid version id")
		return
	}
	for key := range c.Request.URL.Query() {
		if key != "expected_revision_id" {
			apires.RespondBadRequest(c, "unsupported query parameter")
			return
		}
	}
	request := dto.GetTenantCatalogVersionQuery{ExpectedRevisionID: strings.TrimSpace(c.Query("expected_revision_id"))}
	expectedRevisionID := uuid.Nil
	if request.ExpectedRevisionID != "" {
		parsed, parseErr := uuid.Parse(request.ExpectedRevisionID)
		if parseErr != nil || parsed == uuid.Nil {
			apires.RespondBadRequest(c, "invalid expected revision id")
			return
		}
		expectedRevisionID = parsed
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	result, err := h.service.GetTenantCatalogVersion(ctx, &entity.GetTenantCatalogVersion{
		UserID: userID, TenantID: tenantID, WorkspaceID: workspaceID, ZoneID: zoneID, VersionID: versionID, ExpectedRevisionID: expectedRevisionID,
	})
	if err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrCustomerCatalogNotFound):
			apires.RespondNotFound(c, "catalog version not found")
		case errors.Is(err, taxonomy.ErrCustomerCatalogStale):
			apires.RespondConflict(c, "catalog revision changed; refresh the form")
		case errors.Is(err, taxonomy.ErrCustomerCatalogUnavailable):
			c.Header("Retry-After", "2")
			apires.RespondServiceUnavailable(c, "MANAGED_SERVICE_CATALOG_UNAVAILABLE")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "MANAGED_SERVICE_CATALOG_INTERNAL")
		}
		return
	}
	c.Header("Cache-Control", "private, no-store")
	apires.RespondSuccess(c, gin.H{
		"context":      gin.H{"scope": "tenant", "workspace_id": workspaceID, "zone_id": zoneID},
		"category":     gin.H{"id": result.CategoryID, "code": result.CategoryCode, "name_i18n": result.CategoryNameI18n, "description_i18n": result.CategoryDescriptionI18n, "icon_key": result.CategoryIconKey},
		"definition":   gin.H{"id": result.DefinitionID, "code": result.DefinitionCode, "name_i18n": result.DefinitionNameI18n, "description_i18n": result.DefinitionDescriptionI18n, "icon_key": result.DefinitionIconKey},
		"version":      gin.H{"id": result.VersionID, "code": result.VersionCode, "display_version": result.VersionDisplay, "name_i18n": result.VersionNameI18n, "description_i18n": result.VersionDescriptionI18n, "icon_key": result.VersionIconKey},
		"revision":     gin.H{"id": result.RevisionID, "number": result.RevisionNumber, "contract_version": result.ContractVersion, "contract_sha256": hex.EncodeToString(result.ContractSHA256)},
		"input_schema": result.InputSchema, "input_schema_sha256": hex.EncodeToString(result.InputSchemaSHA256),
		"ui_schema": result.UISchema, "ui_schema_sha256": hex.EncodeToString(result.UISchemaSHA256),
	}, "managed service form contract fetched")
}
