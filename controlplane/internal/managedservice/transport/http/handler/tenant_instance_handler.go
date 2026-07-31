package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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

type TenantInstanceHandler struct {
	service managedservice.TenantInstanceService
}

func NewTenantInstanceHandler(service managedservice.TenantInstanceService) *TenantInstanceHandler {
	return &TenantInstanceHandler{service: service}
}

func (h *TenantInstanceHandler) ListTenantInstances(c *gin.Context) {
	const op = "managedservice.tenant_instance.list"
	actorUserID, ok := pkgcontext.GetUserID(c, op)
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

	for key, values := range c.Request.URL.Query() {
		if key != "limit" && key != "cursor" {
			apires.RespondBadRequest(c, "unsupported query parameter")
			return
		}
		if len(values) != 1 {
			apires.RespondBadRequest(c, "query parameter must appear once")
			return
		}
	}
	request := dto.ListTenantInstancesQuery{Limit: strings.TrimSpace(c.Query("limit")), Cursor: strings.TrimSpace(c.Query("cursor"))}
	limit := 50
	if request.Limit != "" {
		parsed, err := strconv.Atoi(request.Limit)
		if err != nil || parsed < 1 || parsed > 100 {
			apires.RespondBadRequest(c, "limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	afterID := uuid.Nil
	if request.Cursor != "" {
		if len(request.Cursor) > 128 {
			apires.RespondBadRequest(c, "invalid cursor")
			return
		}
		decoded, err := base64.RawURLEncoding.DecodeString(request.Cursor)
		if err != nil {
			apires.RespondBadRequest(c, "invalid cursor")
			return
		}
		parsed, err := uuid.Parse(string(decoded))
		if err != nil || parsed == uuid.Nil {
			apires.RespondBadRequest(c, "invalid cursor")
			return
		}
		afterID = parsed
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	result, err := h.service.ListTenantInstances(ctx, &entity.ListTenantInstances{
		ActorUserID: actorUserID, TenantID: tenantID, WorkspaceID: workspaceID, ZoneID: zoneID, AfterID: afterID, Limit: limit,
	})
	if err != nil {
		if errors.Is(err, taxonomy.ErrUnavailable) {
			c.Header("Retry-After", "2")
			apires.RespondServiceUnavailable(c, "managed service instance store unavailable")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal error")
		return
	}

	items := make([]gin.H, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, gin.H{
			"id": item.ID, "code": item.Code, "name": item.Name,
			"desired":          gin.H{"state": item.DesiredState, "generation": item.Generation, "active_revision_id": item.ActiveRevisionID, "pending_revision_id": item.PendingRevisionID},
			"observed":         gin.H{"state": item.ObservedState, "version": item.ObservedStateVersion, "observed_at": item.ObservedAt},
			"metadata_version": item.MetadataVersion,
			"latest_operation": gin.H{"id": item.LatestOperationID, "kind": item.LatestOperationKind, "state": item.LatestOperationState, "generation": item.LatestOperationGen, "attempt": item.LatestOperationTry, "created_at": item.LatestOperationAt},
			"created_at":       item.CreatedAt, "updated_at": item.UpdatedAt,
		})
	}
	nextCursor := ""
	if result.HasMore && len(result.Items) > 0 {
		nextCursor = base64.RawURLEncoding.EncodeToString([]byte(result.Items[len(result.Items)-1].ID.String()))
	}
	c.Header("Cache-Control", "private, no-store")
	apires.RespondSuccess(c, gin.H{
		"context": gin.H{"scope": "tenant", "tenant_id": tenantID, "workspace_id": workspaceID, "zone_id": zoneID},
		"items":   items, "next_cursor": nextCursor,
	}, "managed service instances fetched")
}

func (h *TenantInstanceHandler) GetTenantInstance(c *gin.Context) {
	const op = "managedservice.tenant_instance.get"
	actorUserID, ok := pkgcontext.GetUserID(c, op)
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
	code := strings.ToLower(strings.TrimSpace(c.Param("code")))
	if !regexp.MustCompile(`^[a-z]([a-z0-9-]{0,33}[a-z0-9])?$`).MatchString(code) {
		apires.RespondBadRequest(c, "invalid instance code")
		return
	}
	if len(c.Request.URL.Query()) != 0 {
		apires.RespondBadRequest(c, "unsupported query parameter")
		return
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	result, err := h.service.GetTenantInstance(ctx, &entity.GetTenantInstance{
		ActorUserID: actorUserID, TenantID: tenantID, WorkspaceID: workspaceID, ZoneID: zoneID, Code: code,
	})
	if err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrNotFound):
			apires.RespondNotFound(c, "managed service instance not found")
		case errors.Is(err, taxonomy.ErrUnavailable):
			c.Header("Retry-After", "2")
			apires.RespondServiceUnavailable(c, "managed service instance store unavailable")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal error")
		}
		return
	}

	c.Header("Cache-Control", "private, no-store")
	apires.RespondSuccess(c, gin.H{
		"context": gin.H{"scope": "tenant", "tenant_id": tenantID, "workspace_id": workspaceID, "zone_id": zoneID},
		"instance": gin.H{
			"id": result.ID, "code": result.Code, "name": result.Name,
			"desired":          gin.H{"state": result.DesiredState, "generation": result.Generation, "revision_sequence": result.RevisionSequence, "active_revision_id": result.ActiveRevisionID, "pending_revision_id": result.PendingRevisionID},
			"observed":         gin.H{"state": result.ObservedState, "version": result.ObservedStateVersion, "output": result.ObservedOutput, "observed_at": result.ObservedAt},
			"metadata_version": result.MetadataVersion,
			"latest_operation": gin.H{"id": result.LatestOperationID, "kind": result.LatestOperationKind, "state": result.LatestOperationState, "generation": result.LatestOperationGen, "attempt": result.LatestOperationTry, "created_at": result.LatestOperationAt, "completed_at": result.LatestOperationDoneAt},
			"created_at":       result.CreatedAt, "updated_at": result.UpdatedAt,
		},
	}, "managed service instance fetched")
}

func (h *TenantInstanceHandler) ListTenantInstanceOperations(c *gin.Context) {
	const op = "managedservice.tenant_instance_operation.list"
	actorUserID, ok := pkgcontext.GetUserID(c, op)
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
	code := strings.ToLower(strings.TrimSpace(c.Param("code")))
	if !regexp.MustCompile(`^[a-z]([a-z0-9-]{0,33}[a-z0-9])?$`).MatchString(code) {
		apires.RespondBadRequest(c, "invalid instance code")
		return
	}
	for key, values := range c.Request.URL.Query() {
		if key != "limit" && key != "cursor" {
			apires.RespondBadRequest(c, "unsupported query parameter")
			return
		}
		if len(values) != 1 {
			apires.RespondBadRequest(c, "query parameter must appear once")
			return
		}
	}
	request := dto.ListTenantInstanceOperationsQuery{Limit: strings.TrimSpace(c.Query("limit")), Cursor: strings.TrimSpace(c.Query("cursor"))}
	limit := 50
	if request.Limit != "" {
		parsed, err := strconv.Atoi(request.Limit)
		if err != nil || parsed < 1 || parsed > 100 {
			apires.RespondBadRequest(c, "limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	afterID := uuid.Nil
	if request.Cursor != "" {
		if len(request.Cursor) > 128 {
			apires.RespondBadRequest(c, "invalid cursor")
			return
		}
		decoded, err := base64.RawURLEncoding.DecodeString(request.Cursor)
		if err != nil {
			apires.RespondBadRequest(c, "invalid cursor")
			return
		}
		parsed, err := uuid.Parse(string(decoded))
		if err != nil || parsed == uuid.Nil {
			apires.RespondBadRequest(c, "invalid cursor")
			return
		}
		afterID = parsed
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	result, err := h.service.ListTenantInstanceOperations(ctx, &entity.ListTenantInstanceOperations{
		ActorUserID: actorUserID, TenantID: tenantID, WorkspaceID: workspaceID, ZoneID: zoneID,
		InstanceCode: code, AfterOperationID: afterID, Limit: limit,
	})
	if err != nil {
		if errors.Is(err, taxonomy.ErrUnavailable) {
			c.Header("Retry-After", "2")
			apires.RespondServiceUnavailable(c, "managed service operation store unavailable")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "internal error")
		return
	}

	items := make([]gin.H, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, gin.H{
			"id": item.ID, "kind": item.Kind, "state": item.State, "generation": item.Generation,
			"attempt": item.Attempt, "target_revision_id": item.TargetRevisionID,
			"blueprint_revision_id": item.BlueprintRevisionID, "retry_of_operation_id": item.RetryOfOperationID,
			"status_version": item.StatusVersion, "last_error_code": item.LastErrorCode,
			"last_sanitized_error": item.LastSanitizedError, "completed_at": item.CompletedAt,
			"created_at": item.CreatedAt, "updated_at": item.UpdatedAt,
		})
	}
	nextCursor := ""
	if result.HasMore && len(result.Items) > 0 {
		nextCursor = base64.RawURLEncoding.EncodeToString([]byte(result.Items[len(result.Items)-1].ID.String()))
	}
	c.Header("Cache-Control", "private, no-store")
	apires.RespondSuccess(c, gin.H{
		"context":       gin.H{"scope": "tenant", "tenant_id": tenantID, "workspace_id": workspaceID, "zone_id": zoneID},
		"instance_code": code, "items": items, "next_cursor": nextCursor,
	}, "managed service operations fetched")
}

func (h *TenantInstanceHandler) GetTenantInstanceOperation(c *gin.Context) {
	const op = "managedservice.tenant_instance_operation.get"
	actorUserID, ok := pkgcontext.GetUserID(c, op)
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
	code := strings.ToLower(strings.TrimSpace(c.Param("code")))
	operationID, operationErr := uuid.Parse(strings.TrimSpace(c.Param("operation_id")))
	if !regexp.MustCompile(`^[a-z]([a-z0-9-]{0,33}[a-z0-9])?$`).MatchString(code) || operationErr != nil || operationID == uuid.Nil {
		apires.RespondBadRequest(c, "invalid instance code or operation id")
		return
	}
	if len(c.Request.URL.Query()) != 0 {
		apires.RespondBadRequest(c, "unsupported query parameter")
		return
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	result, err := h.service.GetTenantInstanceOperation(ctx, &entity.GetTenantInstanceOperation{
		ActorUserID: actorUserID, TenantID: tenantID, WorkspaceID: workspaceID, ZoneID: zoneID,
		InstanceCode: code, OperationID: operationID,
	})
	if err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrNotFound):
			apires.RespondNotFound(c, "managed service operation not found")
		case errors.Is(err, taxonomy.ErrUnavailable):
			c.Header("Retry-After", "2")
			apires.RespondServiceUnavailable(c, "managed service operation store unavailable")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal error")
		}
		return
	}

	c.Header("Cache-Control", "private, no-store")
	apires.RespondSuccess(c, gin.H{
		"context":       gin.H{"scope": "tenant", "tenant_id": tenantID, "workspace_id": workspaceID, "zone_id": zoneID},
		"instance_code": code,
		"operation": gin.H{
			"id": result.ID, "instance_id": result.InstanceID, "kind": result.Kind, "state": result.State,
			"generation": result.Generation, "attempt": result.Attempt,
			"target_revision_id": result.TargetRevisionID, "blueprint_revision_id": result.BlueprintRevisionID,
			"retry_of_operation_id": result.RetryOfOperationID, "status_version": result.StatusVersion,
			"last_error_code": result.LastErrorCode, "last_sanitized_error": result.LastSanitizedError,
			"completed_at": result.CompletedAt, "created_at": result.CreatedAt, "updated_at": result.UpdatedAt,
		},
	}, "managed service operation fetched")
}

func (h *TenantInstanceHandler) RenameTenantInstance(c *gin.Context) {
	const op = "managedservice.tenant_instance.rename"
	actorUserID, ok := pkgcontext.GetUserID(c, op)
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
	code := strings.ToLower(strings.TrimSpace(c.Param("code")))
	if !regexp.MustCompile(`^[a-z]([a-z0-9-]{0,33}[a-z0-9])?$`).MatchString(code) {
		apires.RespondBadRequest(c, "invalid instance code")
		return
	}
	if len(c.Request.URL.Query()) != 0 {
		apires.RespondBadRequest(c, "unsupported query parameter")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var request dto.RenameTenantInstanceRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		apires.RespondBadRequest(c, "invalid request")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		apires.RespondBadRequest(c, "request must contain one JSON document")
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	invalidControl := false
	for _, value := range request.Name {
		if unicode.IsControl(value) {
			invalidControl = true
			break
		}
	}
	if request.ExpectedMetadataVersion < 1 || utf8.RuneCountInString(request.Name) < 1 || utf8.RuneCountInString(request.Name) > 160 || invalidControl {
		apires.RespondBadRequest(c, "invalid instance name or metadata version")
		return
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	result, err := h.service.RenameTenantInstance(ctx, &entity.RenameTenantInstance{
		ActorUserID: actorUserID, TenantID: tenantID, WorkspaceID: workspaceID, ZoneID: zoneID,
		Code: code, Name: request.Name, ExpectedMetadataVersion: request.ExpectedMetadataVersion,
	})
	if err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrNotFound):
			apires.RespondNotFound(c, "managed service instance not found")
		case errors.Is(err, taxonomy.ErrConflict):
			apires.RespondConflict(c, "refresh the instance and retry")
		case errors.Is(err, taxonomy.ErrUnavailable):
			c.Header("Retry-After", "2")
			apires.RespondServiceUnavailable(c, "managed service instance store unavailable")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal error")
		}
		return
	}

	c.Header("Cache-Control", "private, no-store")
	apires.RespondSuccess(c, gin.H{
		"id": result.ID, "code": result.Code, "name": result.Name,
		"metadata_version": result.MetadataVersion, "updated_at": result.UpdatedAt,
	}, "managed service instance renamed")
}
