package handler

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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

type PersonalInstanceHandler struct {
	service managedservice.PersonalInstanceService
}

func NewPersonalInstanceHandler(service managedservice.PersonalInstanceService) *PersonalInstanceHandler {
	return &PersonalInstanceHandler{service: service}
}

func (h *PersonalInstanceHandler) ListPersonalInstances(c *gin.Context) {
	const op = "managedservice.personal_instance.list"
	if _, tenantContext := c.Get(pkgcontext.CtxTenantID); tenantContext {
		apires.RespondForbidden(c, "personal instances require personal context")
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
	request := dto.ListPersonalInstancesQuery{Limit: strings.TrimSpace(c.Query("limit")), Cursor: strings.TrimSpace(c.Query("cursor"))}
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
	result, err := h.service.ListPersonalInstances(ctx, &entity.ListPersonalInstances{
		UserID: userID, WorkspaceID: workspaceID, ZoneID: zoneID, AfterID: afterID, Limit: limit,
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
			"state":               item.State,
			"generation":          item.Generation,
			"active_revision_id":  item.ActiveRevisionID,
			"pending_revision_id": item.PendingRevisionID,
			"metadata_version":    item.MetadataVersion,
			"latest_operation":    gin.H{"id": item.LatestOperationID, "kind": item.LatestOperationKind, "state": item.LatestOperationState, "generation": item.LatestOperationGen, "attempt": item.LatestOperationTry, "created_at": item.LatestOperationAt},
			"created_at":          item.CreatedAt, "updated_at": item.UpdatedAt,
		})
	}
	nextCursor := ""
	if result.HasMore && len(result.Items) > 0 {
		nextCursor = base64.RawURLEncoding.EncodeToString([]byte(result.Items[len(result.Items)-1].ID.String()))
	}
	c.Header("Cache-Control", "private, no-store")
	apires.RespondSuccess(c, gin.H{
		"context": gin.H{"scope": "personal", "workspace_id": workspaceID, "zone_id": zoneID},
		"items":   items, "next_cursor": nextCursor,
	}, "managed service instances fetched")
}

func (h *PersonalInstanceHandler) GetPersonalInstance(c *gin.Context) {
	const op = "managedservice.personal_instance.get"
	if _, tenantContext := c.Get(pkgcontext.CtxTenantID); tenantContext {
		apires.RespondForbidden(c, "personal instances require personal context")
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
	result, err := h.service.GetPersonalInstance(ctx, &entity.GetPersonalInstance{UserID: userID, WorkspaceID: workspaceID, ZoneID: zoneID, Code: code})
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
	networkComponents := make([]gin.H, 0, len(result.NetworkContract.Components))
	for _, component := range result.NetworkContract.Components {
		ports := make([]gin.H, 0, len(component.Ports))
		for _, port := range component.Ports {
			ports = append(ports, gin.H{"name": port.Name, "port": port.Port, "protocol": port.Protocol})
		}
		networkComponents = append(networkComponents, gin.H{"component_code": component.ComponentCode, "service_name": component.ServiceName, "pod_selector": component.PodSelector, "ports": ports})
	}
	var resizeContract any
	if result.ResizeContractVersion == "platform-form/v1" && len(result.ResizeInputSchemaHash) == sha256.Size && len(result.ResizeUISchemaHash) == sha256.Size && json.Valid(result.ResizeInputSchema) && json.Valid(result.ResizeUISchema) {
		// [COMMENT]: Only SRE-owned schema metadata crosses this read boundary;
		// prior protected parameter values never return to the browser.
		resizeContract = gin.H{"contract_version": result.ResizeContractVersion, "input_schema": result.ResizeInputSchema, "input_schema_sha256": hex.EncodeToString(result.ResizeInputSchemaHash), "ui_schema": result.ResizeUISchema, "ui_schema_sha256": hex.EncodeToString(result.ResizeUISchemaHash)}
	}
	apires.RespondSuccess(c, gin.H{
		"context": gin.H{"scope": "personal", "workspace_id": workspaceID, "zone_id": zoneID},
		"instance": gin.H{
			"id": result.ID, "code": result.Code, "name": result.Name,
			"state":               result.State,
			"generation":          result.Generation,
			"revision_sequence":   result.RevisionSequence,
			"active_revision_id":  result.ActiveRevisionID,
			"pending_revision_id": result.PendingRevisionID,
			"metadata_version":    result.MetadataVersion,
			"network_contract":    gin.H{"namespace": result.NetworkContract.Namespace, "components": networkComponents},
			"resize_contract":     resizeContract,
			"latest_operation":    gin.H{"id": result.LatestOperationID, "kind": result.LatestOperationKind, "state": result.LatestOperationState, "generation": result.LatestOperationGen, "attempt": result.LatestOperationTry, "created_at": result.LatestOperationAt, "completed_at": result.LatestOperationDoneAt},
			"created_at":          result.CreatedAt, "updated_at": result.UpdatedAt,
		},
	}, "managed service instance fetched")
}

func (h *PersonalInstanceHandler) ListPersonalInstanceOperations(c *gin.Context) {
	const op = "managedservice.personal_instance_operation.list"
	if _, tenantContext := c.Get(pkgcontext.CtxTenantID); tenantContext {
		apires.RespondForbidden(c, "personal instances require personal context")
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
	request := dto.ListPersonalInstanceOperationsQuery{Limit: strings.TrimSpace(c.Query("limit")), Cursor: strings.TrimSpace(c.Query("cursor"))}
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
	result, err := h.service.ListPersonalInstanceOperations(ctx, &entity.ListPersonalInstanceOperations{
		UserID: userID, WorkspaceID: workspaceID, ZoneID: zoneID, InstanceCode: code, AfterOperationID: afterID, Limit: limit,
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
			"attempt": item.Attempt, "delivery_epoch": item.DeliveryEpoch, "target_revision_id": item.TargetRevisionID,
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
		"context":       gin.H{"scope": "personal", "workspace_id": workspaceID, "zone_id": zoneID},
		"instance_code": code, "items": items, "next_cursor": nextCursor,
	}, "managed service operations fetched")
}

func (h *PersonalInstanceHandler) GetPersonalInstanceOperation(c *gin.Context) {
	const op = "managedservice.personal_instance_operation.get"
	if _, tenantContext := c.Get(pkgcontext.CtxTenantID); tenantContext {
		apires.RespondForbidden(c, "personal instances require personal context")
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
	result, err := h.service.GetPersonalInstanceOperation(ctx, &entity.GetPersonalInstanceOperation{
		UserID: userID, WorkspaceID: workspaceID, ZoneID: zoneID, InstanceCode: code, OperationID: operationID,
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
		"context":       gin.H{"scope": "personal", "workspace_id": workspaceID, "zone_id": zoneID},
		"instance_code": code,
		"operation": gin.H{
			"id": result.ID, "instance_id": result.InstanceID, "kind": result.Kind, "state": result.State,
			"generation": result.Generation, "attempt": result.Attempt, "delivery_epoch": result.DeliveryEpoch,
			"target_revision_id": result.TargetRevisionID, "blueprint_revision_id": result.BlueprintRevisionID,
			"retry_of_operation_id": result.RetryOfOperationID, "status_version": result.StatusVersion,
			"last_error_code": result.LastErrorCode, "last_sanitized_error": result.LastSanitizedError,
			"completed_at": result.CompletedAt, "created_at": result.CreatedAt, "updated_at": result.UpdatedAt,
		},
	}, "managed service operation fetched")
}

func (h *PersonalInstanceHandler) RenamePersonalInstance(c *gin.Context) {
	const op = "managedservice.personal_instance.rename"
	if _, tenantContext := c.Get(pkgcontext.CtxTenantID); tenantContext {
		apires.RespondForbidden(c, "personal instances require personal context")
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
	var request dto.RenamePersonalInstanceRequest
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
	result, err := h.service.RenamePersonalInstance(ctx, &entity.RenamePersonalInstance{
		UserID: userID, WorkspaceID: workspaceID, ZoneID: zoneID, Code: code,
		Name: request.Name, ExpectedMetadataVersion: request.ExpectedMetadataVersion,
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

func (h *PersonalInstanceHandler) CreatePersonalInstance(c *gin.Context) {
	const op = "managedservice.personal_instance.create"
	if _, tenantContext := c.Get(pkgcontext.CtxTenantID); tenantContext {
		apires.RespondForbidden(c, "personal instances require personal context")
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
	if len(c.Request.URL.Query()) != 0 {
		apires.RespondBadRequest(c, "unsupported query parameter")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128*1024)
	var request dto.CreatePersonalInstanceRequest
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
	request.Code = strings.ToLower(strings.TrimSpace(request.Code))
	request.Name = strings.TrimSpace(request.Name)
	if !regexp.MustCompile(`^[a-z]([a-z0-9-]{0,33}[a-z0-9])?$`).MatchString(request.Code) || utf8.RuneCountInString(request.Name) < 1 || utf8.RuneCountInString(request.Name) > 160 {
		apires.RespondBadRequest(c, "invalid instance code or name")
		return
	}
	for _, value := range request.Name {
		if unicode.IsControl(value) {
			apires.RespondBadRequest(c, "invalid instance name")
			return
		}
	}
	blueprintRevisionID, err := uuid.Parse(strings.TrimSpace(request.BlueprintRevisionID))
	if err != nil || blueprintRevisionID == uuid.Nil {
		apires.RespondBadRequest(c, "invalid blueprint revision")
		return
	}
	inputSchemaHash, err := hex.DecodeString(strings.TrimSpace(request.InputSchemaSHA256))
	if err != nil || len(inputSchemaHash) != 32 {
		apires.RespondBadRequest(c, "invalid input schema hash")
		return
	}
	if request.Parameters == nil {
		request.Parameters = map[string]json.RawMessage{}
	}
	if len(request.Parameters) > 64 {
		apires.RespondBadRequest(c, "too many parameters")
		return
	}
	for key, raw := range request.Parameters {
		if !regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`).MatchString(key) || len(raw) == 0 || len(raw) > 4096 {
			apires.RespondBadRequest(c, "invalid parameter")
			return
		}
		trimmed := strings.TrimSpace(string(raw))
		if trimmed == "null" || strings.HasPrefix(trimmed, "{") {
			apires.RespondBadRequest(c, "parameters must be flat scalar or scalar-array values")
			return
		}
		if strings.HasPrefix(trimmed, "[") {
			var values []json.RawMessage
			if err := json.Unmarshal(raw, &values); err != nil || len(values) > 64 {
				apires.RespondBadRequest(c, "parameters must be flat scalar or scalar-array values")
				return
			}
			for _, value := range values {
				scalar := strings.TrimSpace(string(value))
				if scalar == "null" || strings.HasPrefix(scalar, "{") || strings.HasPrefix(scalar, "[") {
					apires.RespondBadRequest(c, "parameters must be flat scalar or scalar-array values")
					return
				}
			}
		}
	}
	parameters, err := json.Marshal(request.Parameters)
	if err != nil || len(parameters) > 64*1024 {
		apires.RespondBadRequest(c, "invalid parameters")
		return
	}
	inputSum := sha256.Sum256(parameters)
	desiredSum := sha256.Sum256(append([]byte(workspaceID.String()+":"+zoneID.String()+":"+request.Code+":"+blueprintRevisionID.String()+":1:"), parameters...))
	intentBytes := append([]byte(workspaceID.String()+":"+request.Code+":"+blueprintRevisionID.String()+":"), parameters...)
	intentSum := sha256.Sum256(intentBytes)
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 10*time.Second)
	defer cancel()
	result, err := h.service.CreatePersonalInstance(ctx, &entity.CreatePersonalInstance{UserID: userID, WorkspaceID: workspaceID, ZoneID: zoneID, Code: request.Code, Name: request.Name, BlueprintRevisionID: blueprintRevisionID, InputSchemaSHA256: inputSchemaHash, Parameters: parameters, InputSHA256: inputSum[:], DesiredSpecSHA256: desiredSum[:], CreateIntentSHA256: intentSum[:]})
	if err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrNotFound):
			apires.RespondNotFound(c, "managed service catalog or workspace not found")
		case errors.Is(err, taxonomy.ErrConflict), errors.Is(err, taxonomy.ErrPreconditionFailed):
			apires.RespondConflict(c, "managed service create conflicts with current state")
		case errors.Is(err, taxonomy.ErrUnavailable):
			c.Header("Retry-After", "2")
			apires.RespondServiceUnavailable(c, "managed service instance store unavailable")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal error")
		}
		return
	}
	apires.RespondAccepted(c, gin.H{"instance": gin.H{"id": result.ID, "code": result.Code, "name": result.Name, "state": result.State, "generation": result.Generation, "pending_revision_id": result.PendingRevisionID}, "operation": gin.H{"id": result.OperationID, "kind": result.OperationKind, "state": result.OperationState, "delivery_epoch": result.DeliveryEpoch}}, "managed service instance accepted")
}

func (h *PersonalInstanceHandler) ResizePersonalInstance(c *gin.Context) {
	const op = "managedservice.personal_instance.resize"
	if _, tenantContext := c.Get(pkgcontext.CtxTenantID); tenantContext {
		apires.RespondForbidden(c, "personal instances require personal context")
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
	code := strings.ToLower(strings.TrimSpace(c.Param("code")))
	if !regexp.MustCompile(`^[a-z]([a-z0-9-]{0,33}[a-z0-9])?$`).MatchString(code) {
		apires.RespondBadRequest(c, "invalid instance code")
		return
	}
	if len(c.Request.URL.Query()) != 0 {
		apires.RespondBadRequest(c, "unsupported query parameter")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32*1024)
	var request dto.ResizePersonalInstanceRequest
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
	if request.ExpectedGeneration < 1 || len(request.Resources) == 0 || len(request.Resources) > 32 {
		apires.RespondBadRequest(c, "invalid resize request")
		return
	}
	for key, raw := range request.Resources {
		if !regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`).MatchString(key) || len(raw) == 0 || len(raw) > 4096 {
			apires.RespondBadRequest(c, "invalid resize resource")
			return
		}
		trimmed := strings.TrimSpace(string(raw))
		if trimmed == "null" || strings.HasPrefix(trimmed, "{") {
			apires.RespondBadRequest(c, "resize resources must be scalar or scalar-array values")
			return
		}
		if strings.HasPrefix(trimmed, "[") {
			var values []json.RawMessage
			if err := json.Unmarshal(raw, &values); err != nil || len(values) > 64 {
				apires.RespondBadRequest(c, "resize resources must be scalar or scalar-array values")
				return
			}
			for _, value := range values {
				scalar := strings.TrimSpace(string(value))
				if scalar == "null" || strings.HasPrefix(scalar, "{") || strings.HasPrefix(scalar, "[") {
					apires.RespondBadRequest(c, "resize resources must be scalar or scalar-array values")
					return
				}
			}
		}
	}
	parameters, err := json.Marshal(request.Resources)
	if err != nil || len(parameters) > 64*1024 {
		apires.RespondBadRequest(c, "invalid resize resources")
		return
	}
	inputSum := sha256.Sum256(parameters)
	desiredSum := sha256.Sum256(append([]byte(workspaceID.String()+":"+code+":"+strconv.FormatInt(request.ExpectedGeneration+1, 10)+":"), parameters...))
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 10*time.Second)
	defer cancel()
	result, err := h.service.ResizePersonalInstance(ctx, &entity.ResizePersonalInstance{UserID: userID, WorkspaceID: workspaceID, ZoneID: zoneID, Code: code, ExpectedGeneration: request.ExpectedGeneration, Parameters: parameters, InputSHA256: inputSum[:], DesiredSpecSHA256: desiredSum[:]})
	if err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrNotFound):
			apires.RespondNotFound(c, "managed service instance not found")
		case errors.Is(err, taxonomy.ErrConflict), errors.Is(err, taxonomy.ErrPreconditionFailed):
			apires.RespondConflict(c, "managed service resize conflicts with current state")
		case errors.Is(err, taxonomy.ErrUnavailable):
			c.Header("Retry-After", "2")
			apires.RespondServiceUnavailable(c, "managed service instance store unavailable")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal error")
		}
		return
	}
	apires.RespondAccepted(c, gin.H{"instance": gin.H{"id": result.ID, "code": result.Code, "desired": gin.H{"generation": result.Generation, "pending_revision_id": result.PendingRevisionID}}, "operation": gin.H{"id": result.OperationID, "kind": result.OperationKind, "state": result.OperationState, "delivery_epoch": result.DeliveryEpoch}}, "managed service resize accepted")
}

func (h *PersonalInstanceHandler) DeletePersonalInstance(c *gin.Context) {
	const op = "managedservice.personal_instance.delete"
	if _, tenantContext := c.Get(pkgcontext.CtxTenantID); tenantContext {
		apires.RespondForbidden(c, "personal instances require personal context")
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
	code := strings.ToLower(strings.TrimSpace(c.Param("code")))
	if !regexp.MustCompile(`^[a-z]([a-z0-9-]{0,33}[a-z0-9])?$`).MatchString(code) {
		apires.RespondBadRequest(c, "invalid instance code")
		return
	}
	if len(c.Request.URL.Query()) != 0 {
		apires.RespondBadRequest(c, "unsupported query parameter")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2048)
	var request dto.DeletePersonalInstanceRequest
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
	if request.ExpectedGeneration < 1 {
		apires.RespondBadRequest(c, "invalid expected generation")
		return
	}
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 10*time.Second)
	defer cancel()
	result, err := h.service.DeletePersonalInstance(ctx, &entity.DeletePersonalInstance{UserID: userID, WorkspaceID: workspaceID, ZoneID: zoneID, Code: code, ExpectedGeneration: request.ExpectedGeneration})
	if err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrNotFound):
			apires.RespondNotFound(c, "managed service instance not found")
		case errors.Is(err, taxonomy.ErrConflict), errors.Is(err, taxonomy.ErrPreconditionFailed):
			apires.RespondConflict(c, "managed service delete conflicts with current state")
		case errors.Is(err, taxonomy.ErrUnavailable):
			c.Header("Retry-After", "2")
			apires.RespondServiceUnavailable(c, "managed service instance store unavailable")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal error")
		}
		return
	}
	apires.RespondAccepted(c, gin.H{"instance": gin.H{"id": result.ID, "code": result.Code, "desired": gin.H{"state": "deleting", "generation": result.Generation}}, "operation": gin.H{"id": result.OperationID, "kind": result.OperationKind, "state": result.OperationState, "delivery_epoch": result.DeliveryEpoch}}, "managed service delete accepted")
}

func (h *PersonalInstanceHandler) RetryPersonalInstance(c *gin.Context) {
	const op = "managedservice.personal_instance.retry"
	if _, tenantContext := c.Get(pkgcontext.CtxTenantID); tenantContext {
		apires.RespondForbidden(c, "personal instances require personal context")
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
	code := strings.ToLower(strings.TrimSpace(c.Param("code")))
	operationID, err := uuid.Parse(strings.TrimSpace(c.Param("operation_id")))
	if !regexp.MustCompile(`^[a-z]([a-z0-9-]{0,33}[a-z0-9])?$`).MatchString(code) || err != nil || operationID == uuid.Nil {
		apires.RespondBadRequest(c, "invalid instance code or operation id")
		return
	}
	if len(c.Request.URL.Query()) != 0 {
		apires.RespondBadRequest(c, "unsupported query parameter")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 1024))
	if err != nil || len(strings.TrimSpace(string(body))) != 0 {
		apires.RespondBadRequest(c, "retry request body must be empty")
		return
	}
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 10*time.Second)
	defer cancel()
	result, err := h.service.RetryPersonalInstance(ctx, &entity.RetryPersonalInstance{UserID: userID, WorkspaceID: workspaceID, ZoneID: zoneID, Code: code, OperationID: operationID})
	if err != nil {
		switch {
		case errors.Is(err, taxonomy.ErrNotFound):
			apires.RespondNotFound(c, "managed service operation not found")
		case errors.Is(err, taxonomy.ErrConflict), errors.Is(err, taxonomy.ErrPreconditionFailed):
			apires.RespondConflict(c, "managed service retry conflicts with current state")
		case errors.Is(err, taxonomy.ErrUnavailable):
			c.Header("Retry-After", "2")
			apires.RespondServiceUnavailable(c, "managed service instance store unavailable")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal error")
		}
		return
	}
	apires.RespondAccepted(c, gin.H{"operation": gin.H{"id": result.ID, "instance_id": result.InstanceID, "kind": result.Kind, "state": result.State, "generation": result.Generation, "attempt": result.Attempt, "delivery_epoch": result.DeliveryEpoch}}, "managed service retry accepted")
}
