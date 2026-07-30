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

type PersonalCatalogVersionHandler struct {
	service managedservice.PersonalCatalogVersionService
}

func NewPersonalCatalogVersionHandler(service managedservice.PersonalCatalogVersionService) *PersonalCatalogVersionHandler {
	return &PersonalCatalogVersionHandler{service: service}
}

func (h *PersonalCatalogVersionHandler) GetPersonalCatalogVersion(c *gin.Context) {
	const op = "managedservice.personal_version.get"
	if _, tenantContext := c.Get(pkgcontext.CtxTenantID); tenantContext {
		apires.RespondForbidden(c, "personal catalog requires personal context")
		return
	}
	userID, ok := pkgcontext.GetUserID(c, op)
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
		apires.RespondBadRequestWithCode(c, "REQUEST_INVALID", "invalid version id")
		return
	}
	for key := range c.Request.URL.Query() {
		if key != "expected_revision_id" {
			apires.RespondBadRequestWithCode(c, "REQUEST_INVALID", "unsupported query parameter")
			return
		}
	}
	request := dto.GetPersonalCatalogVersionQuery{ExpectedRevisionID: strings.TrimSpace(c.Query("expected_revision_id"))}
	expectedRevisionID := uuid.Nil
	if request.ExpectedRevisionID != "" {
		parsed, parseErr := uuid.Parse(request.ExpectedRevisionID)
		if parseErr != nil || parsed == uuid.Nil {
			apires.RespondBadRequestWithCode(c, "REQUEST_INVALID", "invalid expected revision id")
			return
		}
		expectedRevisionID = parsed
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	result, err := h.service.GetPersonalCatalogVersion(ctx, &entity.GetPersonalCatalogVersion{
		UserID: userID, WorkspaceID: workspaceID, ZoneID: zoneID, VersionID: versionID, ExpectedRevisionID: expectedRevisionID,
	})
	if err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrCustomerCatalogNotFound):
			apires.RespondNotFoundWithCode(c, "MANAGED_SERVICE_CATALOG_NOT_FOUND", "catalog version not found")
		case errors.Is(err, taxonomy.ErrCustomerCatalogStale):
			apires.RespondConflictWithCode(c, "CATALOG_STALE", "catalog revision changed; refresh the form")
		case errors.Is(err, taxonomy.ErrCustomerCatalogUnavailable):
			c.Header("Retry-After", "2")
			apires.RespondServiceUnavailableWithCode(c, "MANAGED_SERVICE_CATALOG_UNAVAILABLE", "catalog is temporarily unavailable")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "MANAGED_SERVICE_CATALOG_INTERNAL")
		}
		return
	}
	c.Header("Cache-Control", "private, no-store")
	apires.RespondSuccess(c, gin.H{
		"context":      gin.H{"scope": "personal", "workspace_id": workspaceID, "zone_id": zoneID},
		"category":     gin.H{"id": result.CategoryID, "code": result.CategoryCode, "name_i18n": result.CategoryNameI18n, "description_i18n": result.CategoryDescriptionI18n, "icon_key": result.CategoryIconKey},
		"definition":   gin.H{"id": result.DefinitionID, "code": result.DefinitionCode, "name_i18n": result.DefinitionNameI18n, "description_i18n": result.DefinitionDescriptionI18n, "icon_key": result.DefinitionIconKey},
		"version":      gin.H{"id": result.VersionID, "code": result.VersionCode, "display_version": result.VersionDisplay, "name_i18n": result.VersionNameI18n, "description_i18n": result.VersionDescriptionI18n, "icon_key": result.VersionIconKey},
		"revision":     gin.H{"id": result.RevisionID, "number": result.RevisionNumber, "contract_version": result.ContractVersion, "contract_sha256": hex.EncodeToString(result.ContractSHA256)},
		"input_schema": result.InputSchema, "input_schema_sha256": hex.EncodeToString(result.InputSchemaSHA256),
		"ui_schema": result.UISchema, "ui_schema_sha256": hex.EncodeToString(result.UISchemaSHA256),
	}, "managed service form contract fetched")
}
