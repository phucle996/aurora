package handler

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"controlplane/internal/managedservice/domain/entity"
	managedservice "controlplane/internal/managedservice/domain/service"
	"controlplane/internal/managedservice/taxonomy"
	"controlplane/internal/managedservice/transport/http/dto"
	"controlplane/pkg/apires"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type VersionHandler struct{ service managedservice.VersionService }

func NewVersionHandler(service managedservice.VersionService) *VersionHandler {
	return &VersionHandler{service: service}
}

func (h *VersionHandler) CreateVersion(c *gin.Context) {
	// [COMMENT]: Chỉ SRE mới có quyền tạo version catalog.
	actor := strings.TrimSpace(c.GetHeader("x-user-id"))
	if actor != "sre" {
		apires.RespondForbidden(c, "forbidden")
		return
	}

	// [COMMENT]: Giới hạn body 64KB để tránh resource exhaustion.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 65536)
	var request dto.CreateVersionRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	definitionID, definitionErr := uuid.Parse(strings.TrimSpace(request.DefinitionID))
	request.Code = strings.ToLower(strings.TrimSpace(request.Code))
	request.DisplayVersion = strings.TrimSpace(request.DisplayVersion)
	request.IconKey = strings.TrimSpace(request.IconKey)
	english := strings.TrimSpace(request.Name["en"])
	nameJSON, nameErr := json.Marshal(request.Name)
	descriptionJSON, descriptionErr := json.Marshal(request.Description)
	pattern := regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,62}$`)

	// [COMMENT]: Validate definitionID, code, displayVersion bắt buộc, giới hạn kích thước JSON.
	if definitionErr != nil || definitionID == uuid.Nil || !pattern.MatchString(request.Code) || request.DisplayVersion == "" || len(request.DisplayVersion) > 120 || english == "" || len(nameJSON) > 8192 || len(descriptionJSON) > 32768 || len(request.IconKey) > 128 || nameErr != nil || descriptionErr != nil {
		apires.RespondBadRequest(c, "invalid version")
		return
	}

	hash := sha256.Sum256(append(append([]byte(request.Code+request.DisplayVersion), nameJSON...), descriptionJSON...))
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	result, err := h.service.CreateVersion(ctx, &entity.CreateVersion{Actor: actor, DefinitionID: definitionID, Code: request.Code, DisplayVersion: request.DisplayVersion, NameI18n: nameJSON, DescriptionI18n: descriptionJSON, IconKey: request.IconKey, AfterHash: hash[:]})
	if err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrCatalogCodeConflict):
			apires.RespondConflict(c, "version code already exists")
		case errors.Is(err, taxonomy.ErrCatalogNotFound):
			apires.RespondNotFound(c, "definition not found")
		case errors.Is(err, taxonomy.ErrCatalogParentRetired):
			apires.RespondConflict(c, "definition is retired")
		default:
			logger.HandlerError(c, "managedservice.version.create", err)
			apires.RespondInternalError(c, "SRE_CATALOG_INTERNAL")
		}
		return
	}

	apires.RespondCreated(c, gin.H{"id": result.ID, "definition_id": result.DefinitionID, "code": result.Code, "display_version": result.DisplayVersion, "name": result.NameI18n, "description": result.DescriptionI18n, "icon_key": result.IconKey, "state": result.State, "row_version": result.RowVersion}, "version created")
}

func (h *VersionHandler) ListVersions(c *gin.Context) {
	// [COMMENT]: Chỉ SRE mới có quyền list versions.
	if strings.TrimSpace(c.GetHeader("x-user-id")) != "sre" {
		apires.RespondForbidden(c, "forbidden")
		return
	}

	// [COMMENT]: definition_id là optional filter.
	definitionID := uuid.Nil
	if raw := strings.TrimSpace(c.Query("definition_id")); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil {
			apires.RespondBadRequest(c, "invalid definition id")
			return
		}
		definitionID = id
	}

	// [COMMENT]: Default limit 50, tối đa 100.
	limit := 50
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			apires.RespondBadRequest(c, "invalid limit")
			return
		}
		limit = value
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	items, err := h.service.ListVersions(ctx, &entity.ListVersions{DefinitionID: definitionID, Limit: limit})
	if err != nil {
		logger.HandlerError(c, "managedservice.version.list", err)
		apires.RespondInternalError(c, "SRE_CATALOG_INTERNAL")
		return
	}

	rows := make([]gin.H, 0, len(items))
	for _, item := range items {
		rows = append(rows, gin.H{"id": item.ID, "definition_id": item.DefinitionID, "code": item.Code, "display_version": item.DisplayVersion, "name": item.NameI18n, "description": item.DescriptionI18n, "icon_key": item.IconKey, "state": item.State, "row_version": item.RowVersion})
	}

	apires.RespondSuccess(c, gin.H{"items": rows, "count": len(rows)}, "versions fetched")
}

func (h *VersionHandler) GetVersion(c *gin.Context) {
	// [COMMENT]: Chỉ SRE mới có quyền xem chi tiết version.
	if strings.TrimSpace(c.GetHeader("x-user-id")) != "sre" {
		apires.RespondForbidden(c, "forbidden")
		return
	}

	id, err := uuid.Parse(strings.TrimSpace(c.Param("version_id")))
	if err != nil || id == uuid.Nil {
		apires.RespondBadRequest(c, "invalid version id")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	result, err := h.service.GetVersion(ctx, &entity.GetVersion{VersionID: id})
	if err != nil {
		if errors.Is(err, taxonomy.ErrCatalogNotFound) {
			apires.RespondNotFound(c, "version not found")
			return
		}
		logger.HandlerError(c, "managedservice.version.get", err)
		apires.RespondInternalError(c, "SRE_CATALOG_INTERNAL")
		return
	}

	apires.RespondSuccess(c, gin.H{"id": result.ID, "definition_id": result.DefinitionID, "code": result.Code, "display_version": result.DisplayVersion, "name": result.NameI18n, "description": result.DescriptionI18n, "icon_key": result.IconKey, "state": result.State, "row_version": result.RowVersion}, "version fetched")
}

func (h *VersionHandler) UpdateVersion(c *gin.Context) {
	// [COMMENT]: Chỉ SRE mới có quyền update version.
	actor := strings.TrimSpace(c.GetHeader("x-user-id"))
	if actor != "sre" {
		apires.RespondForbidden(c, "forbidden")
		return
	}

	id, err := uuid.Parse(strings.TrimSpace(c.Param("version_id")))
	if err != nil || id == uuid.Nil {
		apires.RespondBadRequest(c, "invalid version id")
		return
	}

	// [COMMENT]: Giới hạn body 64KB để tránh resource exhaustion.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 65536)
	var request dto.UpdateVersionRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	request.DisplayVersion = strings.TrimSpace(request.DisplayVersion)
	request.IconKey = strings.TrimSpace(request.IconKey)
	english := strings.TrimSpace(request.Name["en"])
	nameJSON, nameErr := json.Marshal(request.Name)
	descriptionJSON, descriptionErr := json.Marshal(request.Description)

	// [COMMENT]: ExpectedVersion phải >= 1 để đảm bảo optimistic locking.
	if request.ExpectedVersion < 1 || request.DisplayVersion == "" || len(request.DisplayVersion) > 120 || english == "" || len(nameJSON) > 8192 || len(descriptionJSON) > 32768 || len(request.IconKey) > 128 || nameErr != nil || descriptionErr != nil {
		apires.RespondBadRequest(c, "invalid version")
		return
	}

	hash := sha256.Sum256(append(nameJSON, descriptionJSON...))
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	result, err := h.service.UpdateVersion(ctx, &entity.UpdateVersion{VersionID: id, Actor: actor, ExpectedVersion: request.ExpectedVersion, DisplayVersion: request.DisplayVersion, NameI18n: nameJSON, DescriptionI18n: descriptionJSON, IconKey: request.IconKey, AfterHash: hash[:]})
	if err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrCatalogNotFound):
			apires.RespondNotFound(c, "version not found")
		case errors.Is(err, taxonomy.ErrCatalogConcurrentChange):
			apires.RespondConflict(c, "refresh catalog and retry")
		default:
			// [COMMENT]: Mọi lỗi transition khác đều map sang conflict.
			apires.RespondConflict(c, "version cannot be updated")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{"id": result.ID, "display_version": result.DisplayVersion, "name": result.NameI18n, "description": result.DescriptionI18n, "icon_key": result.IconKey, "state": result.State, "row_version": result.RowVersion}, "version updated")
}

func (h *VersionHandler) DeprecateVersion(c *gin.Context) {
	// [COMMENT]: Deprecate là thao tác critical – bắt buộc có verified proof header.
	actor := strings.TrimSpace(c.GetHeader("x-user-id"))
	proofID, proofErr := uuid.Parse(strings.TrimSpace(c.GetHeader("x-session-proof-challenge-id")))
	if actor != "sre" || c.GetHeader("x-session-proof-verified") != "true" || proofErr != nil || proofID == uuid.Nil {
		apires.RespondForbidden(c, "verified critical proof is required")
		return
	}

	id, err := uuid.Parse(strings.TrimSpace(c.Param("version_id")))
	if err != nil || id == uuid.Nil {
		apires.RespondBadRequest(c, "invalid version id")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var request dto.ChangeVersionStateRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.ExpectedVersion < 1 {
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	result, err := h.service.DeprecateVersion(ctx, &entity.DeprecateVersion{VersionID: id, Actor: actor, ProofID: proofID, ExpectedVersion: request.ExpectedVersion})
	if err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrCatalogNotFound):
			apires.RespondNotFound(c, "version not found")
		case errors.Is(err, taxonomy.ErrCatalogConcurrentChange):
			apires.RespondConflict(c, "refresh catalog and retry")
		default:
			// [COMMENT]: Mọi lỗi transition khác đều map sang conflict.
			apires.RespondConflict(c, "version cannot be deprecated")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{"id": result.ID, "state": result.State, "row_version": result.RowVersion}, "version deprecated")
}

func (h *VersionHandler) RetireVersion(c *gin.Context) {
	// [COMMENT]: Retire là thao tác critical – bắt buộc có verified proof header.
	actor := strings.TrimSpace(c.GetHeader("x-user-id"))
	proofID, proofErr := uuid.Parse(strings.TrimSpace(c.GetHeader("x-session-proof-challenge-id")))
	if actor != "sre" || c.GetHeader("x-session-proof-verified") != "true" || proofErr != nil || proofID == uuid.Nil {
		apires.RespondForbidden(c, "verified critical proof is required")
		return
	}

	id, err := uuid.Parse(strings.TrimSpace(c.Param("version_id")))
	if err != nil || id == uuid.Nil {
		apires.RespondBadRequest(c, "invalid version id")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var request dto.ChangeVersionStateRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.ExpectedVersion < 1 {
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	result, err := h.service.RetireVersion(ctx, &entity.RetireVersion{VersionID: id, Actor: actor, ProofID: proofID, ExpectedVersion: request.ExpectedVersion})
	if err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrCatalogNotFound):
			apires.RespondNotFound(c, "version not found")
		case errors.Is(err, taxonomy.ErrCatalogConcurrentChange):
			apires.RespondConflict(c, "refresh catalog and retry")
		default:
			// [COMMENT]: Mọi lỗi transition khác đều map sang conflict.
			apires.RespondConflict(c, "version cannot be retired")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{"id": result.ID, "state": result.State, "row_version": result.RowVersion}, "version retired")
}
