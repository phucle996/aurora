package mailHandler

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	mailEntity "controlplane/internal/mail/domain/entity"
	mailSvcInterface "controlplane/internal/mail/domain/service"
	mailTaxonomy "controlplane/internal/mail/taxonomy"
	mailReq "controlplane/internal/mail/transport/http/dto/req"
	apires "controlplane/pkg/apires"
	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

type TenantTemplateHandler struct {
	svc mailSvcInterface.TenantTemplateService
}

func NewTenantTemplateHandler(svc mailSvcInterface.TenantTemplateService) *TenantTemplateHandler {
	return &TenantTemplateHandler{svc: svc}
}

func (h *TenantTemplateHandler) Create(c *gin.Context) {
	const op = "mail.tenant.template.create"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 15*time.Second)
	defer cancel()
	actorID, ok := pkgcontext.GetUserID(c, op)
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

	var req mailReq.CreateTemplateRequest
	// [COMMENT]: Inline bind JSON request body với maxBytes limit và strict DisallowUnknownFields check
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 3<<20)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		apires.RespondBadRequest(c, "invalid request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		apires.RespondBadRequest(c, "request body must contain exactly one JSON object")
		return
	}
	if err := binding.Validator.ValidateStruct(&req); err != nil {
		apires.RespondBadRequest(c, "invalid request body")
		return
	}

	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if len(req.IdempotencyKey) < 8 || len(req.IdempotencyKey) > 128 {
		apires.RespondBadRequest(c, "invalid idempotency_key")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.SubjectTemplate = strings.TrimSpace(req.SubjectTemplate)
	if req.Name == "" || len(req.Name) > 255 || req.SubjectTemplate == "" || len(req.SubjectTemplate) > 1024 {
		apires.RespondBadRequest(c, "invalid template name or subject_template")
		return
	}

	template, err := h.svc.CreateTemplate(ctx, &mailEntity.TenantTemplate{ActorUserID: actorID, TenantID: tenantID, WorkspaceID: &workspaceID, ZoneID: zoneID, IdempotencyKey: req.IdempotencyKey, Name: req.Name, SubjectTemplate: req.SubjectTemplate, TextTemplate: req.TextTemplate, HTMLTemplate: req.HTMLTemplate, VariableSchemaJSON: req.VariableSchemaJSON})
	version := template
	if err != nil {
		switch {
		case errors.Is(err, mailTaxonomy.ErrInvalidArgument), errors.Is(err, mailTaxonomy.ErrTemplateSyntax):
			logger.HandlerWarn(c, op, err, "invalid mail request")
			apires.RespondBadRequest(c, "invalid request")
		case errors.Is(err, mailTaxonomy.ErrConsumerNotFound), errors.Is(err, mailTaxonomy.ErrTemplateNotFound), errors.Is(err, mailTaxonomy.ErrWorkspaceNotFound):
			apires.RespondNotFound(c, "mail resource not found")
		case errors.Is(err, mailTaxonomy.ErrAlreadyExists):
			apires.RespondConflict(c, "resource name already exists")
		case errors.Is(err, mailTaxonomy.ErrVersionConflict):
			apires.RespondConflict(c, "resource version changed; reload before retrying")
		case errors.Is(err, mailTaxonomy.ErrIdempotencyConflict):
			apires.RespondConflict(c, "idempotency key was already used with a different request")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	apires.RespondCreated(c, gin.H{
		"template": gin.H{
			"id":                template.ID,
			"workspace_id":      template.WorkspaceID,
			"scope":             template.Scope,
			"name":              template.Name,
			"current_version":   template.CurrentVersion,
			"template_revision": template.TemplateRevision,
			"status":            template.Status,
			"archived_at":       template.ArchivedAt,
			"created_at":        template.CreatedAt,
			"updated_at":        template.UpdatedAt,
		},
		"current_version": gin.H{
			"template_id":          version.TemplateID,
			"version":              version.Version,
			"subject_template":     version.SubjectTemplate,
			"text_template":        version.TextTemplate,
			"html_template":        version.HTMLTemplate,
			"variable_schema_json": version.VariableSchemaJSON,
			"content_sha256":       hex.EncodeToString(version.ContentSHA256),
			"created_at":           version.VersionCreatedAt,
		},
	}, "mail template created")
}

func (h *TenantTemplateHandler) Get(c *gin.Context) {
	const op = "mail.tenant.template.get"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	actorID, ok := pkgcontext.GetUserID(c, op)
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

	templateID := strings.TrimSpace(c.Param("id"))
	if templateID == "" || len(templateID) > 128 {
		apires.RespondBadRequest(c, "invalid template id")
		return
	}

	template, err := h.svc.GetTemplate(ctx, &mailEntity.TenantTemplate{ActorUserID: actorID, TenantID: tenantID, WorkspaceID: &workspaceID, ZoneID: zoneID, ID: templateID})
	version := template
	if err != nil {
		switch {
		case errors.Is(err, mailTaxonomy.ErrInvalidArgument), errors.Is(err, mailTaxonomy.ErrTemplateSyntax):
			logger.HandlerWarn(c, op, err, "invalid mail request")
			apires.RespondBadRequest(c, "invalid request")
		case errors.Is(err, mailTaxonomy.ErrConsumerNotFound), errors.Is(err, mailTaxonomy.ErrTemplateNotFound), errors.Is(err, mailTaxonomy.ErrWorkspaceNotFound):
			apires.RespondNotFound(c, "mail resource not found")
		case errors.Is(err, mailTaxonomy.ErrAlreadyExists):
			apires.RespondConflict(c, "resource name already exists")
		case errors.Is(err, mailTaxonomy.ErrVersionConflict):
			apires.RespondConflict(c, "resource version changed; reload before retrying")
		case errors.Is(err, mailTaxonomy.ErrIdempotencyConflict):
			apires.RespondConflict(c, "idempotency key was already used with a different request")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{
		"template": gin.H{
			"id":                template.ID,
			"workspace_id":      template.WorkspaceID,
			"scope":             template.Scope,
			"name":              template.Name,
			"current_version":   template.CurrentVersion,
			"template_revision": template.TemplateRevision,
			"status":            template.Status,
			"archived_at":       template.ArchivedAt,
			"created_at":        template.CreatedAt,
			"updated_at":        template.UpdatedAt,
		},
		"current_version": gin.H{
			"template_id":          version.TemplateID,
			"version":              version.Version,
			"subject_template":     version.SubjectTemplate,
			"text_template":        version.TextTemplate,
			"html_template":        version.HTMLTemplate,
			"variable_schema_json": version.VariableSchemaJSON,
			"content_sha256":       hex.EncodeToString(version.ContentSHA256),
			"created_at":           version.VersionCreatedAt,
		},
	}, "mail template loaded")
}

func (h *TenantTemplateHandler) List(c *gin.Context) {
	const op = "mail.tenant.template.list"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	actorID, ok := pkgcontext.GetUserID(c, op)
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
	limit := uint64(50)
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		value, err := strconv.ParseUint(raw, 10, 32)
		if err != nil || value == 0 || value > 200 {
			apires.RespondBadRequest(c, "limit must be between 1 and 200")
			return
		}
		limit = value
	}
	templates, err := h.svc.ListTemplates(ctx, &mailEntity.TenantTemplate{ActorUserID: actorID, TenantID: tenantID, WorkspaceID: &workspaceID, ZoneID: zoneID, AfterID: strings.TrimSpace(c.Query("cursor")), Limit: uint32(limit)})
	if err != nil {
		switch {
		case errors.Is(err, mailTaxonomy.ErrInvalidArgument), errors.Is(err, mailTaxonomy.ErrTemplateSyntax):
			logger.HandlerWarn(c, op, err, "invalid mail request")
			apires.RespondBadRequest(c, "invalid request")
		case errors.Is(err, mailTaxonomy.ErrConsumerNotFound), errors.Is(err, mailTaxonomy.ErrTemplateNotFound), errors.Is(err, mailTaxonomy.ErrWorkspaceNotFound):
			apires.RespondNotFound(c, "mail resource not found")
		case errors.Is(err, mailTaxonomy.ErrAlreadyExists):
			apires.RespondConflict(c, "resource name already exists")
		case errors.Is(err, mailTaxonomy.ErrVersionConflict):
			apires.RespondConflict(c, "resource version changed; reload before retrying")
		case errors.Is(err, mailTaxonomy.ErrIdempotencyConflict):
			apires.RespondConflict(c, "idempotency key was already used with a different request")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}
	items := make([]gin.H, 0, len(templates))
	for _, template := range templates {
		items = append(items, gin.H{
			"id":                template.ID,
			"workspace_id":      template.WorkspaceID,
			"scope":             template.Scope,
			"name":              template.Name,
			"current_version":   template.CurrentVersion,
			"template_revision": template.TemplateRevision,
			"status":            template.Status,
			"archived_at":       template.ArchivedAt,
			"created_at":        template.CreatedAt,
			"updated_at":        template.UpdatedAt,
		})
	}
	nextCursor := ""
	if len(templates) == int(limit) {
		nextCursor = templates[len(templates)-1].ID
	}
	apires.RespondSuccess(c, gin.H{"items": items, "next_cursor": nextCursor}, "mail templates loaded")
}

func (h *TenantTemplateHandler) ListVersions(c *gin.Context) {
	const op = "mail.tenant.template.list_versions"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	actorID, ok := pkgcontext.GetUserID(c, op)
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

	templateID := strings.TrimSpace(c.Param("id"))
	if templateID == "" || len(templateID) > 128 {
		apires.RespondBadRequest(c, "invalid template id")
		return
	}

	beforeVersion := uint64(0)
	if raw := strings.TrimSpace(c.Query("cursor")); raw != "" {
		value, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || value == 0 {
			apires.RespondBadRequest(c, "invalid cursor")
			return
		}
		beforeVersion = value
	}
	limit := uint64(50)
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		value, err := strconv.ParseUint(raw, 10, 32)
		if err != nil || value == 0 || value > 200 {
			apires.RespondBadRequest(c, "limit must be between 1 and 200")
			return
		}
		limit = value
	}
	versions, err := h.svc.ListTemplateVersions(ctx, &mailEntity.TenantTemplate{ActorUserID: actorID, TenantID: tenantID, WorkspaceID: &workspaceID, ZoneID: zoneID, ID: templateID, BeforeVersion: beforeVersion, Limit: uint32(limit)})
	if err != nil {
		switch {
		case errors.Is(err, mailTaxonomy.ErrInvalidArgument), errors.Is(err, mailTaxonomy.ErrTemplateSyntax):
			logger.HandlerWarn(c, op, err, "invalid mail request")
			apires.RespondBadRequest(c, "invalid request")
		case errors.Is(err, mailTaxonomy.ErrConsumerNotFound), errors.Is(err, mailTaxonomy.ErrTemplateNotFound), errors.Is(err, mailTaxonomy.ErrWorkspaceNotFound):
			apires.RespondNotFound(c, "mail resource not found")
		case errors.Is(err, mailTaxonomy.ErrAlreadyExists):
			apires.RespondConflict(c, "resource name already exists")
		case errors.Is(err, mailTaxonomy.ErrVersionConflict):
			apires.RespondConflict(c, "resource version changed; reload before retrying")
		case errors.Is(err, mailTaxonomy.ErrIdempotencyConflict):
			apires.RespondConflict(c, "idempotency key was already used with a different request")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}
	items := make([]gin.H, 0, len(versions))
	for _, version := range versions {
		items = append(items, gin.H{
			"template_id":          version.TemplateID,
			"version":              version.Version,
			"subject_template":     version.SubjectTemplate,
			"text_template":        version.TextTemplate,
			"html_template":        version.HTMLTemplate,
			"variable_schema_json": version.VariableSchemaJSON,
			"content_sha256":       hex.EncodeToString(version.ContentSHA256),
			"created_at":           version.VersionCreatedAt,
		})
	}
	nextCursor := uint64(0)
	if len(versions) == int(limit) {
		nextCursor = versions[len(versions)-1].Version
	}
	apires.RespondSuccess(c, gin.H{"items": items, "next_cursor": nextCursor}, "mail template versions loaded")
}

func (h *TenantTemplateHandler) PublishVersion(c *gin.Context) {
	const op = "mail.tenant.template.publish_version"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 15*time.Second)
	defer cancel()
	actorID, ok := pkgcontext.GetUserID(c, op)
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

	templateID := strings.TrimSpace(c.Param("id"))
	if templateID == "" || len(templateID) > 128 {
		apires.RespondBadRequest(c, "invalid template id")
		return
	}

	var req mailReq.PublishTemplateVersionRequest
	// [COMMENT]: Inline bind JSON request body với maxBytes limit và strict DisallowUnknownFields check
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 3<<20)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		apires.RespondBadRequest(c, "invalid request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		apires.RespondBadRequest(c, "request body must contain exactly one JSON object")
		return
	}
	if err := binding.Validator.ValidateStruct(&req); err != nil {
		apires.RespondBadRequest(c, "invalid request body")
		return
	}

	// [COMMENT]: HTTP boundary normalize và chặn payload quá lớn/header injection trước khi vào service compiler.
	req.SubjectTemplate = strings.TrimSpace(req.SubjectTemplate)
	if req.ExpectedRevision == 0 || req.SubjectTemplate == "" || len(req.SubjectTemplate) > 998 ||
		strings.ContainsAny(req.SubjectTemplate, "\r\n") ||
		(strings.TrimSpace(req.TextTemplate) == "" && strings.TrimSpace(req.HTMLTemplate) == "") ||
		len(req.TextTemplate) > 1<<20 || len(req.HTMLTemplate) > 1<<20 || len(req.VariableSchemaJSON) > 64<<10 {
		apires.RespondBadRequest(c, "invalid publish parameters")
		return
	}

	template, err := h.svc.PublishTemplateVersion(ctx, &mailEntity.TenantTemplate{ActorUserID: actorID, TenantID: tenantID, WorkspaceID: &workspaceID, ZoneID: zoneID, TemplateID: templateID, ExpectedRevision: req.ExpectedRevision, SubjectTemplate: req.SubjectTemplate, TextTemplate: req.TextTemplate, HTMLTemplate: req.HTMLTemplate, VariableSchemaJSON: req.VariableSchemaJSON})
	version := template
	if err != nil {
		switch {
		case errors.Is(err, mailTaxonomy.ErrInvalidArgument), errors.Is(err, mailTaxonomy.ErrTemplateSyntax):
			logger.HandlerWarn(c, op, err, "invalid mail request")
			apires.RespondBadRequest(c, "invalid request")
		case errors.Is(err, mailTaxonomy.ErrConsumerNotFound), errors.Is(err, mailTaxonomy.ErrTemplateNotFound), errors.Is(err, mailTaxonomy.ErrWorkspaceNotFound):
			apires.RespondNotFound(c, "mail resource not found")
		case errors.Is(err, mailTaxonomy.ErrAlreadyExists):
			apires.RespondConflict(c, "resource name already exists")
		case errors.Is(err, mailTaxonomy.ErrVersionConflict):
			apires.RespondConflict(c, "resource version changed; reload before retrying")
		case errors.Is(err, mailTaxonomy.ErrIdempotencyConflict):
			apires.RespondConflict(c, "idempotency key was already used with a different request")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	apires.RespondCreated(c, gin.H{
		"template": gin.H{
			"id":                template.ID,
			"workspace_id":      template.WorkspaceID,
			"scope":             template.Scope,
			"name":              template.Name,
			"current_version":   template.CurrentVersion,
			"template_revision": template.TemplateRevision,
			"status":            template.Status,
			"archived_at":       template.ArchivedAt,
			"created_at":        template.CreatedAt,
			"updated_at":        template.UpdatedAt,
		},
		"published_version": gin.H{
			"template_id":          version.TemplateID,
			"version":              version.Version,
			"subject_template":     version.SubjectTemplate,
			"text_template":        version.TextTemplate,
			"html_template":        version.HTMLTemplate,
			"variable_schema_json": version.VariableSchemaJSON,
			"content_sha256":       hex.EncodeToString(version.ContentSHA256),
			"created_at":           version.VersionCreatedAt,
		},
	}, "mail template version published")
}

func (h *TenantTemplateHandler) Archive(c *gin.Context) {
	const op = "mail.tenant.template.archive"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 10*time.Second)
	defer cancel()
	actorID, ok := pkgcontext.GetUserID(c, op)
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
	templateID := strings.TrimSpace(c.Param("id"))
	if templateID == "" || len(templateID) > 128 {
		apires.RespondBadRequest(c, "invalid template id")
		return
	}

	var req mailReq.ArchiveTemplateRequest
	// [COMMENT]: Inline bind JSON request body với maxBytes limit và strict DisallowUnknownFields check
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		apires.RespondBadRequest(c, "invalid request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		apires.RespondBadRequest(c, "request body must contain exactly one JSON object")
		return
	}
	if err := binding.Validator.ValidateStruct(&req); err != nil {
		apires.RespondBadRequest(c, "invalid request body")
		return
	}

	if req.ExpectedRevision == 0 {
		apires.RespondBadRequest(c, "expected_revision is required")
		return
	}

	err := h.svc.ArchiveTemplate(ctx, &mailEntity.TenantTemplate{ActorUserID: actorID, TenantID: tenantID, WorkspaceID: &workspaceID, ZoneID: zoneID, TemplateID: templateID, ExpectedRevision: req.ExpectedRevision})
	if err != nil {
		switch {
		case errors.Is(err, mailTaxonomy.ErrInvalidArgument), errors.Is(err, mailTaxonomy.ErrTemplateSyntax):
			logger.HandlerWarn(c, op, err, "invalid mail request")
			apires.RespondBadRequest(c, "invalid request")
		case errors.Is(err, mailTaxonomy.ErrConsumerNotFound), errors.Is(err, mailTaxonomy.ErrTemplateNotFound), errors.Is(err, mailTaxonomy.ErrWorkspaceNotFound):
			apires.RespondNotFound(c, "mail resource not found")
		case errors.Is(err, mailTaxonomy.ErrAlreadyExists):
			apires.RespondConflict(c, "resource name already exists")
		case errors.Is(err, mailTaxonomy.ErrVersionConflict):
			apires.RespondConflict(c, "resource version changed; reload before retrying")
		case errors.Is(err, mailTaxonomy.ErrIdempotencyConflict):
			apires.RespondConflict(c, "idempotency key was already used with a different request")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}
	apires.RespondAccepted(c, gin.H{"template_id": templateID, "status": mailEntity.TemplateArchived}, "mail template archived")
}
