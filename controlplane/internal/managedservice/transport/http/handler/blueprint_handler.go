package handler

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
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

type BlueprintHandler struct {
	service managedservice.BlueprintService
}

func NewBlueprintHandler(service managedservice.BlueprintService) *BlueprintHandler {
	return &BlueprintHandler{service: service}
}

func (h *BlueprintHandler) CreateBlueprint(c *gin.Context) {
	const op = "managedservice.blueprint.create"

	// [COMMENT]: CreateBlueprint là thao tác critical – bắt buộc có verified proof header.
	actor := strings.TrimSpace(c.GetHeader("x-user-id"))
	proofID, proofErr := uuid.Parse(strings.TrimSpace(c.GetHeader("x-session-proof-challenge-id")))
	if actor != "sre" || c.GetHeader("x-session-proof-verified") != "true" || proofErr != nil || proofID == uuid.Nil {
		apires.RespondForbidden(c, "verified critical proof is required")
		return
	}

	versionID, versionErr := uuid.Parse(strings.TrimSpace(c.Param("version_id")))
	if versionErr != nil || versionID == uuid.Nil {
		apires.RespondBadRequest(c, "invalid version id")
		return
	}

	// [COMMENT]: Giới hạn body 64KB để tránh resource exhaustion.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 65536)
	var request dto.CreateBlueprintRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	request.Code = strings.ToLower(strings.TrimSpace(request.Code))
	request.IconKey = strings.TrimSpace(request.IconKey)
	english := strings.TrimSpace(request.Name["en"])
	nameJSON, nameErr := json.Marshal(request.Name)
	descriptionJSON, descriptionErr := json.Marshal(request.Description)
	pattern := regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)

	// [COMMENT]: Validate code, tên tiếng Anh bắt buộc, giới hạn kích thước JSON.
	if !pattern.MatchString(request.Code) || english == "" || len(english) > 160 || len(nameJSON) > 8192 || len(descriptionJSON) > 32768 || len(request.IconKey) > 128 || nameErr != nil || descriptionErr != nil {
		apires.RespondBadRequest(c, "invalid blueprint")
		return
	}

	hash := sha256.Sum256(append(append([]byte(request.Code), nameJSON...), descriptionJSON...))
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	result, err := h.service.CreateBlueprint(ctx, &entity.CreateBlueprint{Actor: actor, ProofID: proofID, VersionID: versionID, Code: request.Code, Name: english, NameI18n: nameJSON, DescriptionI18n: descriptionJSON, IconKey: request.IconKey, AfterHash: hash[:]})
	if err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrCatalogCodeConflict):
			apires.RespondConflict(c, "version already has a blueprint")
		case errors.Is(err, taxonomy.ErrCatalogNotFound):
			apires.RespondNotFound(c, "version not found")
		case errors.Is(err, taxonomy.ErrCatalogParentRetired):
			apires.RespondConflict(c, "version is not available")
		default:
			logger.HandlerError(c, "managedservice.blueprint.create", err)
			apires.RespondInternalError(c, "SRE_CATALOG_INTERNAL")
		}
		return
	}

	apires.RespondCreated(c, gin.H{
		"id":                    result.ID,
		"version_id":            result.VersionID,
		"code":                  result.Code,
		"name":                  result.NameI18n,
		"description":           result.DescriptionI18n,
		"icon_key":              result.IconKey,
		"state":                 result.State,
		"row_version":           result.RowVersion,
		"published_revision_id": result.PublishedRevisionID,
	}, "blueprint created")
}

func (h *BlueprintHandler) GetBlueprint(c *gin.Context) {
	const op = "managedservice.blueprint.get"

	// [COMMENT]: Chỉ SRE mới có quyền xem chi tiết blueprint.
	if strings.TrimSpace(c.GetHeader("x-user-id")) != "sre" {
		apires.RespondForbidden(c, "forbidden")
		return
	}

	id, err := uuid.Parse(strings.TrimSpace(c.Param("blueprint_id")))
	if err != nil || id == uuid.Nil {
		apires.RespondBadRequest(c, "invalid blueprint id")
		return
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	result, err := h.service.GetBlueprint(ctx, &entity.GetBlueprint{BlueprintID: id})
	if err != nil {
		if errors.Is(err, taxonomy.ErrCatalogNotFound) {
			apires.RespondNotFound(c, "blueprint not found")
			return
		}
		logger.HandlerError(c, "managedservice.blueprint.get", err)
		apires.RespondInternalError(c, "SRE_CATALOG_INTERNAL")
		return
	}

	apires.RespondSuccess(c, gin.H{
		"id":                    result.ID,
		"version_id":            result.VersionID,
		"code":                  result.Code,
		"name":                  result.NameI18n,
		"description":           result.DescriptionI18n,
		"icon_key":              result.IconKey,
		"state":                 result.State,
		"row_version":           result.RowVersion,
		"published_revision_id": result.PublishedRevisionID,
	}, "blueprint fetched")
}

func (h *BlueprintHandler) GetBlueprintByVersion(c *gin.Context) {
	const op = "managedservice.blueprint.get_by_version"

	// [COMMENT]: Chỉ SRE mới có quyền xem blueprint theo version.
	if strings.TrimSpace(c.GetHeader("x-user-id")) != "sre" {
		apires.RespondForbidden(c, "forbidden")
		return
	}

	versionID, err := uuid.Parse(strings.TrimSpace(c.Param("version_id")))
	if err != nil || versionID == uuid.Nil {
		apires.RespondBadRequest(c, "invalid version id")
		return
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	result, err := h.service.GetBlueprintByVersion(ctx, &entity.GetBlueprintByVersion{VersionID: versionID})
	if err != nil {
		if errors.Is(err, taxonomy.ErrCatalogNotFound) {
			apires.RespondNotFound(c, "blueprint not found")
			return
		}
		logger.HandlerError(c, "managedservice.blueprint.get_by_version", err)
		apires.RespondInternalError(c, "SRE_CATALOG_INTERNAL")
		return
	}

	apires.RespondSuccess(c, gin.H{
		"id":                    result.ID,
		"version_id":            result.VersionID,
		"code":                  result.Code,
		"name":                  result.NameI18n,
		"description":           result.DescriptionI18n,
		"icon_key":              result.IconKey,
		"state":                 result.State,
		"row_version":           result.RowVersion,
		"published_revision_id": result.PublishedRevisionID,
	}, "blueprint fetched")
}

func (h *BlueprintHandler) DeleteBlueprint(c *gin.Context) {
	const op = "managedservice.blueprint.delete"

	// [COMMENT]: Delete blueprint là thao tác critical – bắt buộc có verified proof header.
	actor := strings.TrimSpace(c.GetHeader("x-user-id"))
	proofID, proofErr := uuid.Parse(strings.TrimSpace(c.GetHeader("x-session-proof-challenge-id")))
	if actor != "sre" || c.GetHeader("x-session-proof-verified") != "true" || proofErr != nil || proofID == uuid.Nil {
		apires.RespondForbidden(c, "verified critical proof is required")
		return
	}

	id, err := uuid.Parse(strings.TrimSpace(c.Param("blueprint_id")))
	if err != nil || id == uuid.Nil {
		apires.RespondBadRequest(c, "invalid blueprint id")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var request dto.DeleteBlueprintRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.ExpectedVersion < 1 {
		apires.RespondBadRequest(c, "invalid request")
		return
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	err = h.service.DeleteBlueprint(ctx, &entity.DeleteBlueprint{BlueprintID: id, Actor: actor, ProofID: proofID, ExpectedVersion: request.ExpectedVersion})
	if err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrCatalogNotFound):
			apires.RespondNotFound(c, "blueprint not found")
		case errors.Is(err, taxonomy.ErrCatalogConcurrentChange):
			apires.RespondConflict(c, "refresh catalog and retry")
		case errors.Is(err, taxonomy.ErrCatalogRecordPinned):
			apires.RespondConflict(c, "blueprint has revisions")
		default:
			// [COMMENT]: Mọi lỗi immutable/transition khác đều là conflict.
			apires.RespondConflict(c, "blueprint cannot be deleted")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{
		"id": id},
		"blueprint deleted")
}
