package handler

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
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

type TenantCatalogHandler struct {
	service managedservice.TenantCatalogService
}

func NewTenantCatalogHandler(service managedservice.TenantCatalogService) *TenantCatalogHandler {
	return &TenantCatalogHandler{service: service}
}

func (h *TenantCatalogHandler) ListTenantCatalog(c *gin.Context) {
	const op = "managedservice.tenant_catalog.list"
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

	for key := range c.Request.URL.Query() {
		if key != "limit" && key != "cursor" {
			apires.RespondBadRequestWithCode(c, "REQUEST_INVALID", "unsupported query parameter")
			return
		}
	}
	request := dto.ListTenantCatalogQuery{Limit: strings.TrimSpace(c.Query("limit")), Cursor: strings.TrimSpace(c.Query("cursor"))}
	limit := 50
	if request.Limit != "" {
		parsed, err := strconv.Atoi(request.Limit)
		if err != nil || parsed < 1 || parsed > 100 {
			apires.RespondBadRequestWithCode(c, "REQUEST_INVALID", "limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	afterVersionID := uuid.Nil
	if request.Cursor != "" {
		if len(request.Cursor) > 128 {
			apires.RespondBadRequestWithCode(c, "REQUEST_INVALID", "invalid cursor")
			return
		}
		decoded, err := base64.RawURLEncoding.DecodeString(request.Cursor)
		if err != nil {
			apires.RespondBadRequestWithCode(c, "REQUEST_INVALID", "invalid cursor")
			return
		}
		parsed, err := uuid.Parse(string(decoded))
		if err != nil || parsed == uuid.Nil {
			apires.RespondBadRequestWithCode(c, "REQUEST_INVALID", "invalid cursor")
			return
		}
		afterVersionID = parsed
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	result, err := h.service.ListTenantCatalog(ctx, &entity.ListTenantCatalog{
		UserID: userID, TenantID: tenantID, WorkspaceID: workspaceID, ZoneID: zoneID, AfterVersionID: afterVersionID, Limit: limit,
	})
	if err != nil {
		if errors.Is(err, taxonomy.ErrCustomerCatalogUnavailable) {
			c.Header("Retry-After", "2")
			apires.RespondServiceUnavailableWithCode(c, "MANAGED_SERVICE_CATALOG_UNAVAILABLE", "catalog is temporarily unavailable")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "MANAGED_SERVICE_CATALOG_INTERNAL")
		return
	}

	items := make([]gin.H, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, gin.H{
			"category":   gin.H{"id": item.CategoryID, "code": item.CategoryCode, "name_i18n": item.CategoryNameI18n, "description_i18n": item.CategoryDescriptionI18n, "icon_key": item.CategoryIconKey},
			"definition": gin.H{"id": item.DefinitionID, "code": item.DefinitionCode, "name_i18n": item.DefinitionNameI18n, "description_i18n": item.DefinitionDescriptionI18n, "icon_key": item.DefinitionIconKey},
			"version":    gin.H{"id": item.VersionID, "code": item.VersionCode, "display_version": item.VersionDisplay, "name_i18n": item.VersionNameI18n, "description_i18n": item.VersionDescriptionI18n, "icon_key": item.VersionIconKey},
			"revision":   gin.H{"id": item.RevisionID, "number": item.RevisionNumber, "contract_version": item.ContractVersion, "contract_sha256": hex.EncodeToString(item.ContractSHA256)},
		})
	}
	nextCursor := ""
	if result.HasMore && len(result.Items) > 0 {
		nextCursor = base64.RawURLEncoding.EncodeToString([]byte(result.Items[len(result.Items)-1].VersionID.String()))
	}
	c.Header("Cache-Control", "private, no-store")
	apires.RespondSuccess(c, gin.H{
		"context": gin.H{"scope": "tenant", "workspace_id": workspaceID, "zone_id": zoneID},
		"items":   items, "next_cursor": nextCursor,
	}, "managed service catalog fetched")
}
