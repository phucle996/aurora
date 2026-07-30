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

type CategoryHandler struct {
	service managedservice.CategoryService
}

func NewCategoryHandler(service managedservice.CategoryService) *CategoryHandler {
	return &CategoryHandler{service: service}
}

func (h *CategoryHandler) CreateCategory(c *gin.Context) {
	const op = "managedservice.category.create"

	// [COMMENT]: Chỉ SRE mới có quyền tạo category catalog.
	actor := strings.TrimSpace(c.GetHeader("x-user-id"))
	if actor != "sre" {
		apires.RespondForbidden(c, "forbidden")
		return
	}

	// [COMMENT]: Giới hạn body 64KB để tránh resource exhaustion.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 65536)
	var request dto.CreateCategoryRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	request.Code = strings.ToLower(strings.TrimSpace(request.Code))
	request.IconKey = strings.TrimSpace(request.IconKey)
	codePattern := regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)
	english := strings.TrimSpace(request.Name["en"])
	nameJSON, nameErr := json.Marshal(request.Name)
	descriptionJSON, descriptionErr := json.Marshal(request.Description)

	// [COMMENT]: Validate code pattern, tên tiếng Anh bắt buộc, giới hạn kích thước JSON.
	if !codePattern.MatchString(request.Code) || english == "" || len(english) > 160 || len(nameJSON) > 8192 || len(descriptionJSON) > 32768 || len(request.IconKey) > 128 || nameErr != nil || descriptionErr != nil {
		apires.RespondBadRequest(c, "invalid catalog metadata")
		return
	}

	hash := sha256.Sum256(append(append(append([]byte(request.Code), nameJSON...), descriptionJSON...), []byte(request.IconKey)...))
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	result, err := h.service.CreateCategory(ctx, &entity.CreateCategory{Actor: actor, Code: request.Code, Name: english, Description: strings.TrimSpace(request.Description["en"]), NameI18n: nameJSON, DescriptionI18n: descriptionJSON, IconKey: request.IconKey, AfterHash: hash[:]})
	if err != nil {
		if errors.Is(err, taxonomy.ErrCatalogCodeConflict) {
			apires.RespondConflict(c, "catalog code already exists")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "SRE_CATALOG_INTERNAL")
		return
	}

	apires.RespondCreated(c, gin.H{
		"id":          result.ID,
		"code":        result.Code,
		"name":        result.NameI18n,
		"description": result.DescriptionI18n,
		"icon_key":    result.IconKey,
		"state":       result.State,
		"row_version": result.RowVersion,
		"created_at":  result.CreatedAt},
		"category created")
}

func (h *CategoryHandler) ListCategories(c *gin.Context) {
	// [COMMENT]: Chỉ SRE mới có quyền list categories.
	actor := strings.TrimSpace(c.GetHeader("x-user-id"))
	if actor != "sre" {
		apires.RespondForbidden(c, "forbidden")
		return
	}

	// [COMMENT]: Default limit 50, tối đa 100.
	limit := 50
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			apires.RespondBadRequest(c, "invalid limit")
			return
		}
		limit = parsed
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	items, err := h.service.ListCategories(ctx, &entity.ListCategories{Limit: limit})
	if err != nil {
		logger.HandlerError(c, "managedservice.category.list", err)
		apires.RespondInternalError(c, "SRE_CATALOG_INTERNAL")
		return
	}

	rows := make([]gin.H, 0, len(items))
	for _, item := range items {
		rows = append(rows, gin.H{
			"id":          item.ID,
			"code":        item.Code,
			"name":        item.NameI18n,
			"description": item.DescriptionI18n,
			"icon_key":    item.IconKey,
			"state":       item.State,
			"row_version": item.RowVersion,
			"created_at":  item.CreatedAt,
			"updated_at":  item.UpdatedAt,
		})
	}

	apires.RespondSuccess(c, gin.H{"items": rows, "count": len(rows)}, "categories fetched")
}

func (h *CategoryHandler) GetCategory(c *gin.Context) {
	// [COMMENT]: Chỉ SRE mới có quyền xem chi tiết category.
	actor := strings.TrimSpace(c.GetHeader("x-user-id"))
	if actor != "sre" {
		apires.RespondForbidden(c, "forbidden")
		return
	}

	id, err := uuid.Parse(strings.TrimSpace(c.Param("category_id")))
	if err != nil || id == uuid.Nil {
		apires.RespondBadRequest(c, "invalid category id")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	result, err := h.service.GetCategory(ctx, &entity.GetCategory{CategoryID: id})
	if err != nil {
		if errors.Is(err, taxonomy.ErrCatalogNotFound) {
			apires.RespondNotFound(c, "category not found")
			return
		}
		logger.HandlerError(c, "managedservice.category.get", err)
		apires.RespondInternalError(c, "SRE_CATALOG_INTERNAL")
		return
	}

	apires.RespondSuccess(c, gin.H{"id": result.ID, "code": result.Code, "name": result.NameI18n, "description": result.DescriptionI18n, "icon_key": result.IconKey, "state": result.State, "row_version": result.RowVersion, "created_at": result.CreatedAt, "updated_at": result.UpdatedAt}, "category fetched")
}

func (h *CategoryHandler) UpdateCategory(c *gin.Context) {
	// [COMMENT]: Chỉ SRE mới có quyền update category.
	actor := strings.TrimSpace(c.GetHeader("x-user-id"))
	if actor != "sre" {
		apires.RespondForbidden(c, "forbidden")
		return
	}

	id, err := uuid.Parse(strings.TrimSpace(c.Param("category_id")))
	if err != nil || id == uuid.Nil {
		apires.RespondBadRequest(c, "invalid category id")
		return
	}

	// [COMMENT]: Giới hạn body 64KB để tránh resource exhaustion.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 65536)
	var request dto.UpdateCategoryRequest
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
		apires.RespondBadRequest(c, "invalid catalog metadata")
		return
	}

	hash := sha256.Sum256(append(append(nameJSON, descriptionJSON...), []byte(request.IconKey)...))
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	result, err := h.service.UpdateCategory(ctx, &entity.UpdateCategory{CategoryID: id, Actor: actor, ExpectedVersion: request.ExpectedVersion, Name: english, Description: strings.TrimSpace(request.Description["en"]), NameI18n: nameJSON, DescriptionI18n: descriptionJSON, IconKey: request.IconKey, AfterHash: hash[:]})
	if err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrCatalogNotFound):
			apires.RespondNotFound(c, "category not found")
		case errors.Is(err, taxonomy.ErrCatalogConcurrentChange):
			apires.RespondConflict(c, "refresh catalog and retry")
		case errors.Is(err, taxonomy.ErrCatalogInvalidTransition):
			apires.RespondConflict(c, "category cannot be updated")
		default:
			logger.HandlerError(c, "managedservice.category.update", err)
			apires.RespondInternalError(c, "SRE_CATALOG_INTERNAL")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{
		"id":          result.ID,
		"code":        result.Code,
		"name":        result.NameI18n,
		"description": result.DescriptionI18n,
		"icon_key":    result.IconKey,
		"state":       result.State,
		"row_version": result.RowVersion,
		"updated_at":  result.UpdatedAt},
		"category updated")
}

func (h *CategoryHandler) RetireCategory(c *gin.Context) {
	// [COMMENT]: Retire là thao tác critical – bắt buộc phải có verified proof header.
	actor := strings.TrimSpace(c.GetHeader("x-user-id"))
	proofID, proofErr := uuid.Parse(strings.TrimSpace(c.GetHeader("x-session-proof-challenge-id")))
	if actor != "sre" || c.GetHeader("x-session-proof-verified") != "true" || proofErr != nil || proofID == uuid.Nil {
		apires.RespondForbidden(c, "verified critical proof is required")
		return
	}

	id, err := uuid.Parse(strings.TrimSpace(c.Param("category_id")))
	if err != nil || id == uuid.Nil {
		apires.RespondBadRequest(c, "invalid category id")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var request dto.RetireCategoryRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.ExpectedVersion < 1 {
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	result, err := h.service.RetireCategory(ctx, &entity.RetireCategory{CategoryID: id, Actor: actor, ProofID: proofID, ExpectedVersion: request.ExpectedVersion})
	if err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrCatalogNotFound):
			apires.RespondNotFound(c, "category not found")
		case errors.Is(err, taxonomy.ErrCatalogConcurrentChange):
			apires.RespondConflict(c, "refresh catalog and retry")
		default:
			// [COMMENT]: Mọi lỗi transition khác đều map sang conflict – category không thể retire.
			apires.RespondConflict(c, "category cannot be retired")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{
		"id":          result.ID,
		"state":       result.State,
		"row_version": result.RowVersion},
		"category retired")
}
