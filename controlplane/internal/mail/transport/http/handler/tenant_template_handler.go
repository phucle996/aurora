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

	req.Code = strings.ToLower(strings.TrimSpace(req.Code))
	validCode := len(req.Code) >= 3 && len(req.Code) <= 63 && req.Code[0] >= 'a' && req.Code[0] <= 'z'
	for index, char := range req.Code {
		if !((char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || (char == '-' && index > 0 && index < len(req.Code)-1 && req.Code[index-1] != '-')) {
			validCode = false
			break
		}
	}
	if !validCode {
		apires.RespondBadRequest(c, "invalid template code")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.SubjectTemplate = strings.TrimSpace(req.SubjectTemplate)
	if req.Name == "" || len(req.Name) > 255 || req.SubjectTemplate == "" || len(req.SubjectTemplate) > 1024 {
		apires.RespondBadRequest(c, "invalid template name or subject_template")
		return
	}

	res, err := h.svc.CreateTemplate(ctx, &mailEntity.CreateTenantTemplateRequest{
		ActorUserID: actorID, TenantID: tenantID, WorkspaceID: workspaceID, ZoneID: zoneID, Code: req.Code, Name: req.Name, SubjectTemplate: req.SubjectTemplate, RawHTML: req.HTMLTemplate,
	})
	if err != nil {
		switch {
		case errors.Is(err, mailTaxonomy.ErrInvalidArgument), errors.Is(err, mailTaxonomy.ErrTemplateSyntax):
			logger.HandlerWarn(c, op, err, "invalid mail request")
			apires.RespondBadRequest(c, "invalid request")
		case errors.Is(err, mailTaxonomy.ErrConsumerNotFound), errors.Is(err, mailTaxonomy.ErrTemplateNotFound), errors.Is(err, mailTaxonomy.ErrWorkspaceNotFound):
			apires.RespondNotFound(c, "mail resource not found")
		case errors.Is(err, mailTaxonomy.ErrAlreadyExists):
			apires.RespondConflict(c, "resource name already exists")
		case errors.Is(err, mailTaxonomy.ErrVersionConflict), errors.Is(err, mailTaxonomy.ErrOperationInProgress):
			apires.RespondConflict(c, "resource version changed; reload before retrying")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	apires.RespondAccepted(c, gin.H{
		"operation_id": res.OperationID.String(),
		"template": gin.H{
			"id":                res.ID,
			"workspace_id":      res.WorkspaceID,
			"code":              res.Code,
			"name":              res.Name,
			"current_version":   res.CurrentVersion,
			"template_revision": res.TemplateRevision,
			"created_at":        res.CreatedAt,
			"updated_at":        res.UpdatedAt,
		},
		"current_version": gin.H{
			"template_id":      res.ID,
			"version":          res.CurrentVersion,
			"subject_template": res.SubjectTemplate,
			"html_template":    res.RawHTML,
			"content_sha256":   hex.EncodeToString(res.ContentSHA256),
			"created_at":       res.CreatedAt,
		},
	}, "mail template creation scheduled")
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

	res, err := h.svc.GetTemplate(ctx, &mailEntity.GetTenantTemplateRequest{
		ActorUserID: actorID, TenantID: tenantID, WorkspaceID: workspaceID, ZoneID: zoneID, TemplateID: templateID,
	})
	if err != nil {
		switch {
		case errors.Is(err, mailTaxonomy.ErrInvalidArgument), errors.Is(err, mailTaxonomy.ErrTemplateSyntax):
			logger.HandlerWarn(c, op, err, "invalid mail request")
			apires.RespondBadRequest(c, "invalid request")
		case errors.Is(err, mailTaxonomy.ErrConsumerNotFound), errors.Is(err, mailTaxonomy.ErrTemplateNotFound), errors.Is(err, mailTaxonomy.ErrWorkspaceNotFound):
			apires.RespondNotFound(c, "mail resource not found")
		case errors.Is(err, mailTaxonomy.ErrAlreadyExists):
			apires.RespondConflict(c, "resource name already exists")
		case errors.Is(err, mailTaxonomy.ErrVersionConflict), errors.Is(err, mailTaxonomy.ErrOperationInProgress):
			apires.RespondConflict(c, "resource version changed; reload before retrying")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{
		"template": gin.H{
			"id":                res.ID,
			"workspace_id":      res.WorkspaceID,
			"code":              res.Code,
			"name":              res.Name,
			"current_version":   res.CurrentVersion,
			"template_revision": res.TemplateRevision,
			"created_at":        res.CreatedAt,
			"updated_at":        res.UpdatedAt,
		},
		"current_version": gin.H{
			"template_id":      res.ID,
			"version":          res.CurrentVersion,
			"subject_template": res.SubjectTemplate,
			"html_template":    res.RawHTML,
			"content_sha256":   hex.EncodeToString(res.ContentSHA256),
			"created_at":       res.CreatedAt,
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

	items, err := h.svc.ListTemplates(ctx, &mailEntity.ListTenantTemplatesRequest{
		ActorUserID: actorID, TenantID: tenantID, WorkspaceID: workspaceID, ZoneID: zoneID, AfterID: strings.TrimSpace(c.Query("cursor")), Limit: uint32(limit),
	})
	if err != nil {
		switch {
		case errors.Is(err, mailTaxonomy.ErrInvalidArgument), errors.Is(err, mailTaxonomy.ErrTemplateSyntax):
			logger.HandlerWarn(c, op, err, "invalid mail request")
			apires.RespondBadRequest(c, "invalid request")
		case errors.Is(err, mailTaxonomy.ErrConsumerNotFound), errors.Is(err, mailTaxonomy.ErrTemplateNotFound), errors.Is(err, mailTaxonomy.ErrWorkspaceNotFound):
			apires.RespondNotFound(c, "mail resource not found")
		case errors.Is(err, mailTaxonomy.ErrAlreadyExists):
			apires.RespondConflict(c, "resource name already exists")
		case errors.Is(err, mailTaxonomy.ErrVersionConflict), errors.Is(err, mailTaxonomy.ErrOperationInProgress):
			apires.RespondConflict(c, "resource version changed; reload before retrying")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}
	list := make([]gin.H, 0, len(items))
	for _, item := range items {
		list = append(list, gin.H{
			"id":                item.ID,
			"workspace_id":      item.WorkspaceID,
			"code":              item.Code,
			"name":              item.Name,
			"current_version":   item.CurrentVersion,
			"template_revision": item.TemplateRevision,
			"created_at":        item.CreatedAt,
			"updated_at":        item.UpdatedAt,
		})
	}
	nextCursor := ""
	if len(items) == int(limit) {
		nextCursor = items[len(items)-1].ID
	}
	apires.RespondSuccess(c, gin.H{"items": list, "next_cursor": nextCursor}, "mail templates loaded")
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

	items, err := h.svc.ListTemplateVersions(ctx, &mailEntity.ListTenantTemplateVersionsRequest{
		ActorUserID: actorID, TenantID: tenantID, WorkspaceID: workspaceID, ZoneID: zoneID, TemplateID: templateID, BeforeVersion: beforeVersion, Limit: uint32(limit),
	})
	if err != nil {
		switch {
		case errors.Is(err, mailTaxonomy.ErrInvalidArgument), errors.Is(err, mailTaxonomy.ErrTemplateSyntax):
			logger.HandlerWarn(c, op, err, "invalid mail request")
			apires.RespondBadRequest(c, "invalid request")
		case errors.Is(err, mailTaxonomy.ErrConsumerNotFound), errors.Is(err, mailTaxonomy.ErrTemplateNotFound), errors.Is(err, mailTaxonomy.ErrWorkspaceNotFound):
			apires.RespondNotFound(c, "mail resource not found")
		case errors.Is(err, mailTaxonomy.ErrAlreadyExists):
			apires.RespondConflict(c, "resource name already exists")
		case errors.Is(err, mailTaxonomy.ErrVersionConflict), errors.Is(err, mailTaxonomy.ErrOperationInProgress):
			apires.RespondConflict(c, "resource version changed; reload before retrying")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}
	list := make([]gin.H, 0, len(items))
	for _, item := range items {
		list = append(list, gin.H{
			"template_id":      item.TemplateID,
			"version":          item.Version,
			"subject_template": item.SubjectTemplate,
			"html_template":    item.RawHTML,
			"content_sha256":   hex.EncodeToString(item.ContentSHA256),
			"created_at":       item.CreatedAt,
		})
	}
	nextCursor := uint64(0)
	if len(items) == int(limit) {
		nextCursor = items[len(items)-1].Version
	}
	apires.RespondSuccess(c, gin.H{"items": list, "next_cursor": nextCursor}, "mail template versions loaded")
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

	req.SubjectTemplate = strings.TrimSpace(req.SubjectTemplate)
	if req.ExpectedRevision == 0 || req.SubjectTemplate == "" || len(req.SubjectTemplate) > 998 ||
		strings.ContainsAny(req.SubjectTemplate, "\r\n") ||
		strings.TrimSpace(req.HTMLTemplate) == "" || len(req.HTMLTemplate) > 1<<20 {
		apires.RespondBadRequest(c, "invalid publish parameters")
		return
	}

	res, err := h.svc.PublishTemplateVersion(ctx, &mailEntity.PublishTenantTemplateVersionRequest{
		ActorUserID: actorID, TenantID: tenantID, WorkspaceID: workspaceID, ZoneID: zoneID, TemplateID: templateID, ExpectedRevision: req.ExpectedRevision, SubjectTemplate: req.SubjectTemplate, RawHTML: req.HTMLTemplate,
	})
	if err != nil {
		switch {
		case errors.Is(err, mailTaxonomy.ErrInvalidArgument), errors.Is(err, mailTaxonomy.ErrTemplateSyntax):
			logger.HandlerWarn(c, op, err, "invalid mail request")
			apires.RespondBadRequest(c, "invalid request")
		case errors.Is(err, mailTaxonomy.ErrConsumerNotFound), errors.Is(err, mailTaxonomy.ErrTemplateNotFound), errors.Is(err, mailTaxonomy.ErrWorkspaceNotFound):
			apires.RespondNotFound(c, "mail resource not found")
		case errors.Is(err, mailTaxonomy.ErrAlreadyExists):
			apires.RespondConflict(c, "resource name already exists")
		case errors.Is(err, mailTaxonomy.ErrVersionConflict), errors.Is(err, mailTaxonomy.ErrOperationInProgress):
			apires.RespondConflict(c, "resource version changed; reload before retrying")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	// [COMMENT]: template.current_revision = active head revision (chưa thay đổi);
	// published_version.revision = candidate revision vừa được ghi — phân biệt rõ để JO có thể promote đúng.
	apires.RespondAccepted(c, gin.H{
		"operation_id": res.OperationID.String(),
		"template": gin.H{
			"id":               res.ID,
			"workspace_id":     res.WorkspaceID,
			"code":             res.Code,
			"name":             res.Name,
			"current_version":  res.CurrentVersion,
			"current_revision": res.CurrentRevision,
			"created_at":       res.HeadCreatedAt,
		},
		"published_version": gin.H{
			"template_id":      res.ID,
			"version":          res.PublishedVersion,
			"revision":         res.PublishedRevision,
			"subject_template": res.SubjectTemplate,
			"html_template":    res.RawHTML,
			"content_sha256":   hex.EncodeToString(res.ContentSHA256),
			"created_at":       res.CandidateCreatedAt,
		},
	}, "mail template publish scheduled")
}

func (h *TenantTemplateHandler) Delete(c *gin.Context) {
	const op = "mail.tenant.template.delete"
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
	var req mailReq.DeleteTemplateRequest
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

	operationID, err := h.svc.DeleteTemplate(ctx, &mailEntity.DeleteTenantTemplateRequest{
		ActorUserID: actorID, TenantID: tenantID, WorkspaceID: workspaceID, ZoneID: zoneID, TemplateID: templateID, ExpectedRevision: req.ExpectedRevision,
	})
	if err != nil {
		switch {
		case errors.Is(err, mailTaxonomy.ErrInvalidArgument), errors.Is(err, mailTaxonomy.ErrTemplateSyntax):
			logger.HandlerWarn(c, op, err, "invalid mail request")
			apires.RespondBadRequest(c, "invalid request")
		case errors.Is(err, mailTaxonomy.ErrConsumerNotFound), errors.Is(err, mailTaxonomy.ErrTemplateNotFound), errors.Is(err, mailTaxonomy.ErrWorkspaceNotFound):
			apires.RespondNotFound(c, "mail resource not found")
		case errors.Is(err, mailTaxonomy.ErrAlreadyExists):
			apires.RespondConflict(c, "resource name already exists")
		case errors.Is(err, mailTaxonomy.ErrVersionConflict), errors.Is(err, mailTaxonomy.ErrOperationInProgress):
			apires.RespondConflict(c, "resource version changed; reload before retrying")
		case errors.Is(err, mailTaxonomy.ErrTemplateInUse):
			apires.RespondConflict(c, "template is still used by an active consumer")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}
	apires.RespondAccepted(c, gin.H{"template_id": templateID, "operation_id": operationID.String()}, "mail template deletion scheduled")
}
