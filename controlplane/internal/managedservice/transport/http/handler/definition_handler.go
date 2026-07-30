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
	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type DefinitionHandler struct {
	service managedservice.DefinitionService
}

func NewDefinitionHandler(service managedservice.DefinitionService) *DefinitionHandler {
	return &DefinitionHandler{service: service}
}

func (h *DefinitionHandler) CreateDefinition(c *gin.Context) {
	const op = "managedservice.definition.create"

	// [COMMENT]: Chỉ SRE mới có quyền tạo definition catalog.
	actor := strings.TrimSpace(c.GetHeader("x-user-id"))
	if actor != "sre" {
		apires.RespondForbidden(c, "forbidden")
		return
	}

	// [COMMENT]: Giới hạn body 64KB để tránh resource exhaustion.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 65536)
	var request dto.CreateDefinitionRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	categoryID, categoryErr := uuid.Parse(strings.TrimSpace(request.CategoryID))
	request.Code = strings.ToLower(strings.TrimSpace(request.Code))
	english := strings.TrimSpace(request.Name["en"])
	request.IconKey = strings.TrimSpace(request.IconKey)
	nameJSON, nameErr := json.Marshal(request.Name)
	descriptionJSON, descriptionErr := json.Marshal(request.Description)
	pattern := regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)

	// [COMMENT]: Validate categoryID, code pattern, tên tiếng Anh bắt buộc, giới hạn kích thước JSON.
	if categoryErr != nil || categoryID == uuid.Nil || !pattern.MatchString(request.Code) || english == "" || len(english) > 160 || len(nameJSON) > 8192 || len(descriptionJSON) > 32768 || len(request.IconKey) > 128 || nameErr != nil || descriptionErr != nil {
		apires.RespondBadRequest(c, "invalid definition")
		return
	}

	hash := sha256.Sum256(append(append([]byte(request.Code), nameJSON...), descriptionJSON...))
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	result, err := h.service.CreateDefinition(ctx, &entity.CreateDefinition{Actor: actor, CategoryID: categoryID, Code: request.Code, Name: english, Description: strings.TrimSpace(request.Description["en"]), NameI18n: nameJSON, DescriptionI18n: descriptionJSON, IconKey: request.IconKey, AfterHash: hash[:]})
	if err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrCatalogCodeConflict):
			apires.RespondConflict(c, "definition code already exists")
		case errors.Is(err, taxonomy.ErrCatalogNotFound):
			apires.RespondNotFound(c, "category not found")
		case errors.Is(err, taxonomy.ErrCatalogParentRetired):
			apires.RespondConflict(c, "category is retired")
		default:
			logger.HandlerError(c, "managedservice.definition.create", err)
			apires.RespondInternalError(c, "SRE_CATALOG_INTERNAL")
		}
		return
	}

	apires.RespondCreated(c, gin.H{"id": result.ID, "category_id": result.CategoryID, "code": result.Code, "name": result.NameI18n, "description": result.DescriptionI18n, "icon_key": result.IconKey, "state": result.State, "row_version": result.RowVersion}, "definition created")
}

func (h *DefinitionHandler) ListDefinitions(c *gin.Context) {
	const op = "managedservice.definition.list"

	// [COMMENT]: Chỉ SRE mới có quyền list definitions.
	if strings.TrimSpace(c.GetHeader("x-user-id")) != "sre" {
		apires.RespondForbidden(c, "forbidden")
		return
	}

	// [COMMENT]: category_id là optional filter.
	categoryID := uuid.Nil
	if raw := strings.TrimSpace(c.Query("category_id")); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil {
			apires.RespondBadRequest(c, "invalid category id")
			return
		}
		categoryID = id
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

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	items, err := h.service.ListDefinitions(ctx, &entity.ListDefinitions{CategoryID: categoryID, Limit: limit})
	if err != nil {
		logger.HandlerError(c, "managedservice.definition.list", err)
		apires.RespondInternalError(c, "SRE_CATALOG_INTERNAL")
		return
	}

	rows := make([]gin.H, 0, len(items))
	for _, item := range items {
		rows = append(rows, gin.H{
			"id":          item.ID,
			"category_id": item.CategoryID,
			"code":        item.Code,
			"name":        item.NameI18n,
			"description": item.DescriptionI18n,
			"icon_key":    item.IconKey,
			"state":       item.State,
			"row_version": item.RowVersion})
	}

	apires.RespondSuccess(c, gin.H{"items": rows, "count": len(rows)}, "definitions fetched")
}

func (h *DefinitionHandler) GetDefinition(c *gin.Context) {
	const op = "managedservice.definition.get"

	// [COMMENT]: Chỉ SRE mới có quyền xem chi tiết definition.
	if strings.TrimSpace(c.GetHeader("x-user-id")) != "sre" {
		apires.RespondForbidden(c, "forbidden")
		return
	}

	id, err := uuid.Parse(strings.TrimSpace(c.Param("definition_id")))
	if err != nil || id == uuid.Nil {
		apires.RespondBadRequest(c, "invalid definition id")
		return
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	result, err := h.service.GetDefinition(ctx, &entity.GetDefinition{DefinitionID: id})
	if err != nil {
		if errors.Is(err, taxonomy.ErrCatalogNotFound) {
			apires.RespondNotFound(c, "definition not found")
			return
		}
		logger.HandlerError(c, "managedservice.definition.get", err)
		apires.RespondInternalError(c, "SRE_CATALOG_INTERNAL")
		return
	}

	apires.RespondSuccess(c, gin.H{
		"id":          result.ID,
		"category_id": result.CategoryID,
		"code":        result.Code,
		"name":        result.NameI18n,
		"description": result.DescriptionI18n,
		"icon_key":    result.IconKey,
		"state":       result.State,
		"row_version": result.RowVersion},
		"definition fetched")
}

func (h *DefinitionHandler) UpdateDefinition(c *gin.Context) {
	const op = "managedservice.definition.update"

	// [COMMENT]: Chỉ SRE mới có quyền update definition.
	actor := strings.TrimSpace(c.GetHeader("x-user-id"))
	if actor != "sre" {
		apires.RespondForbidden(c, "forbidden")
		return
	}

	id, err := uuid.Parse(strings.TrimSpace(c.Param("definition_id")))
	if err != nil || id == uuid.Nil {
		apires.RespondBadRequest(c, "invalid definition id")
		return
	}

	// [COMMENT]: Giới hạn body 64KB để tránh resource exhaustion.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 65536)
	var request dto.UpdateDefinitionRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	english := strings.TrimSpace(request.Name["en"])
	request.IconKey = strings.TrimSpace(request.IconKey)
	nameJSON, nameErr := json.Marshal(request.Name)
	descriptionJSON, descriptionErr := json.Marshal(request.Description)

	// [COMMENT]: ExpectedVersion phải >= 1 để đảm bảo optimistic locking.
	if request.ExpectedVersion < 1 || english == "" || len(english) > 160 || len(nameJSON) > 8192 || len(descriptionJSON) > 32768 || len(request.IconKey) > 128 || nameErr != nil || descriptionErr != nil {
		apires.RespondBadRequest(c, "invalid definition")
		return
	}

	hash := sha256.Sum256(append(nameJSON, descriptionJSON...))
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	result, err := h.service.UpdateDefinition(ctx, &entity.UpdateDefinition{DefinitionID: id, Actor: actor, ExpectedVersion: request.ExpectedVersion, Name: english, Description: strings.TrimSpace(request.Description["en"]), NameI18n: nameJSON, DescriptionI18n: descriptionJSON, IconKey: request.IconKey, AfterHash: hash[:]})
	if err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrCatalogNotFound):
			apires.RespondNotFound(c, "definition not found")
		case errors.Is(err, taxonomy.ErrCatalogConcurrentChange):
			apires.RespondConflict(c, "refresh catalog and retry")
		default:
			// [COMMENT]: Mọi lỗi transition khác đều map sang conflict.
			apires.RespondConflict(c, "definition cannot be updated")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{
		"id":          result.ID,
		"name":        result.NameI18n,
		"description": result.DescriptionI18n,
		"icon_key":    result.IconKey,
		"state":       result.State,
		"row_version": result.RowVersion},
		"definition updated")
}

func (h *DefinitionHandler) RetireDefinition(c *gin.Context) {
	const op = "managedservice.definition.retire"

	// [COMMENT]: Retire là thao tác critical – bắt buộc có verified proof header.
	actor := strings.TrimSpace(c.GetHeader("x-user-id"))
	proofID, proofErr := uuid.Parse(strings.TrimSpace(c.GetHeader("x-session-proof-challenge-id")))
	if actor != "sre" || c.GetHeader("x-session-proof-verified") != "true" || proofErr != nil || proofID == uuid.Nil {
		apires.RespondForbidden(c, "verified critical proof is required")
		return
	}

	id, err := uuid.Parse(strings.TrimSpace(c.Param("definition_id")))
	if err != nil || id == uuid.Nil {
		apires.RespondBadRequest(c, "invalid definition id")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var request dto.RetireDefinitionRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.ExpectedVersion < 1 {
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	result, err := h.service.RetireDefinition(ctx, &entity.RetireDefinition{DefinitionID: id, Actor: actor, ProofID: proofID, ExpectedVersion: request.ExpectedVersion})
	if err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrCatalogNotFound):
			apires.RespondNotFound(c, "definition not found")
		case errors.Is(err, taxonomy.ErrCatalogConcurrentChange):
			apires.RespondConflict(c, "refresh catalog and retry")
		default:
			// [COMMENT]: Mọi lỗi transition khác đều map sang conflict.
			apires.RespondConflict(c, "definition cannot be retired")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{
		"id":          result.ID,
		"state":       result.State,
		"row_version": result.RowVersion},
		"definition retired")
}
