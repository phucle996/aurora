package mailHandler

import (
	"context"
	"encoding/base64"
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
	"github.com/google/uuid"
)

type TenantConsumerHandler struct {
	svc mailSvcInterface.TenantConsumerService
}

func NewTenantConsumerHandler(svc mailSvcInterface.TenantConsumerService) *TenantConsumerHandler {
	return &TenantConsumerHandler{svc: svc}
}

func (h *TenantConsumerHandler) Create(c *gin.Context) {
	const op = "mail.tenant.consumer.create"
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

	var req mailReq.CreateConsumerRequest
	// [COMMENT]: Inline bind JSON request body với maxBytes limit và strict DisallowUnknownFields check
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10)
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

	brokerID, err := uuid.Parse(strings.TrimSpace(req.BrokerResourceID))
	if err != nil {
		apires.RespondBadRequest(c, "invalid broker_resource_id")
		return
	}
	sourceConfigEnvelope, err := base64.StdEncoding.DecodeString(strings.TrimSpace(req.SourceConfigEnvelope))
	if err != nil || len(sourceConfigEnvelope) > 16<<10 {
		apires.RespondBadRequest(c, "invalid source_config_envelope")
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
		apires.RespondBadRequest(c, "invalid consumer code")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Topic = strings.TrimSpace(req.Topic)
	req.ConsumerGroup = strings.TrimSpace(req.ConsumerGroup)
	req.TemplateID = strings.TrimSpace(req.TemplateID)
	req.SenderProfileID = strings.TrimSpace(req.SenderProfileID)
	req.Mapping.RecipientJSONPath = strings.TrimSpace(req.Mapping.RecipientJSONPath)
	req.Mapping.ExternalMessageIDJSONPath = strings.TrimSpace(req.Mapping.ExternalMessageIDJSONPath)

	if req.Name == "" || len(req.Name) > 255 || req.Topic == "" || len(req.Topic) > 249 ||
		req.ConsumerGroup == "" || len(req.ConsumerGroup) > 255 ||
		req.TemplateID == "" || len(req.TemplateID) > 128 || req.SenderProfileID == "" || len(req.SenderProfileID) > 128 {
		apires.RespondBadRequest(c, "invalid consumer name, topic, group, template_id, or sender_profile_id")
		return
	}

	for _, brokerName := range []string{req.Topic, req.ConsumerGroup} {
		for _, char := range brokerName {
			if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
				(char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-') {
				apires.RespondBadRequest(c, "invalid characters in topic or consumer_group")
				return
			}
		}
	}

	if !strings.HasPrefix(req.Mapping.RecipientJSONPath, "$") || len(req.Mapping.RecipientJSONPath) > 512 {
		apires.RespondBadRequest(c, "invalid recipient_json_path")
		return
	}
	if req.Mapping.ExternalMessageIDJSONPath != "" && (!strings.HasPrefix(req.Mapping.ExternalMessageIDJSONPath, "$") || len(req.Mapping.ExternalMessageIDJSONPath) > 512) {
		apires.RespondBadRequest(c, "invalid external_message_id_json_path")
		return
	}

	consumer, err := h.svc.CreateConsumer(ctx, &mailEntity.TenantConsumer{ActorUserID: actorID, TenantID: tenantID, WorkspaceID: workspaceID, ZoneID: zoneID,
		Code: req.Code, Name: req.Name,
		SourceType: req.SourceType, BrokerResourceID: brokerID,
		SourceConfigEnvelope: sourceConfigEnvelope,
		Topic:                req.Topic, ConsumerGroup: req.ConsumerGroup,
		Mapping:    mailEntity.MessageMapping{ExternalMessageIDJSONPath: req.Mapping.ExternalMessageIDJSONPath, RecipientJSONPath: req.Mapping.RecipientJSONPath, VariableJSONPaths: req.Mapping.VariableJSONPaths},
		TemplateID: req.TemplateID, TemplateVersion: req.TemplateVersion,
		SenderProfileID: req.SenderProfileID, SenderVersion: req.SenderVersion, Parallelism: req.Parallelism,
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
		case errors.Is(err, mailTaxonomy.ErrVersionConflict):
			apires.RespondConflict(c, "resource version changed; reload before retrying")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	apires.RespondCreated(c, gin.H{
		"id":                 consumer.ID.String(),
		"workspace_id":       consumer.WorkspaceID.String(),
		"code":               consumer.Code,
		"name":               consumer.Name,
		"source_type":        consumer.SourceType,
		"broker_resource_id": consumer.BrokerResourceID.String(),
		"source_configured":  len(consumer.SourceConfigEnvelope) > 0,
		"topic":              consumer.Topic,
		"consumer_group":     consumer.ConsumerGroup,
		"mapping":            consumer.MappingJSON,
		"template_id":        consumer.TemplateID,
		"template_version":   consumer.TemplateVersion,
		"sender_profile_id":  consumer.SenderProfileID,
		"sender_version":     consumer.SenderVersion,
		"desired_state":      consumer.DesiredState,
		"parallelism":        consumer.Parallelism,
		"config_version":     consumer.ConfigVersion,
		"config_sha256":      hex.EncodeToString(consumer.ConfigSHA256),
		"created_at":         consumer.CreatedAt,
		"updated_at":         consumer.UpdatedAt,
	}, "mail consumer created in paused state")
}

func (h *TenantConsumerHandler) Get(c *gin.Context) {
	const op = "mail.tenant.consumer.get"
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

	consumerID, err := uuid.Parse(strings.TrimSpace(c.Param("id")))
	if err != nil {
		apires.RespondBadRequest(c, "invalid consumer id")
		return
	}

	consumer, err := h.svc.GetConsumer(ctx, &mailEntity.TenantConsumer{ActorUserID: actorID, TenantID: tenantID, WorkspaceID: workspaceID, ZoneID: zoneID, ID: consumerID})
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
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{
		"id":                 consumer.ID.String(),
		"workspace_id":       consumer.WorkspaceID.String(),
		"code":               consumer.Code,
		"name":               consumer.Name,
		"source_type":        consumer.SourceType,
		"broker_resource_id": consumer.BrokerResourceID.String(),
		"source_configured":  len(consumer.SourceConfigEnvelope) > 0,
		"topic":              consumer.Topic,
		"consumer_group":     consumer.ConsumerGroup,
		"mapping":            consumer.MappingJSON,
		"template_id":        consumer.TemplateID,
		"template_version":   consumer.TemplateVersion,
		"sender_profile_id":  consumer.SenderProfileID,
		"sender_version":     consumer.SenderVersion,
		"desired_state":      consumer.DesiredState,
		"parallelism":        consumer.Parallelism,
		"config_version":     consumer.ConfigVersion,
		"config_sha256":      hex.EncodeToString(consumer.ConfigSHA256),
		"created_at":         consumer.CreatedAt,
		"updated_at":         consumer.UpdatedAt,
	}, "mail consumer loaded")
}

func (h *TenantConsumerHandler) List(c *gin.Context) {
	const op = "mail.tenant.consumer.list"
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

	var source *mailEntity.SourceType
	if raw := strings.TrimSpace(c.Query("source_type")); raw != "" {
		value := mailEntity.SourceType(raw)
		if value != mailEntity.Kafka {
			apires.RespondBadRequest(c, "invalid source_type")
			return
		}
		source = &value
	}
	var state *mailEntity.ConsumerDesiredState
	if raw := strings.TrimSpace(c.Query("desired_state")); raw != "" {
		value := mailEntity.ConsumerDesiredState(raw)
		if value != mailEntity.ConsumerPaused && value != mailEntity.ConsumerEnabled && value != mailEntity.ConsumerDeleting {
			apires.RespondBadRequest(c, "invalid desired_state")
			return
		}
		state = &value
	}
	var afterID *uuid.UUID
	if raw := strings.TrimSpace(c.Query("cursor")); raw != "" {
		value, err := uuid.Parse(raw)
		if err != nil {
			apires.RespondBadRequest(c, "invalid cursor")
			return
		}
		afterID = &value
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
	consumers, err := func() ([]*mailEntity.TenantConsumer, error) {
		query := &mailEntity.TenantConsumer{ActorUserID: actorID, TenantID: tenantID, WorkspaceID: workspaceID, ZoneID: zoneID, AfterID: afterID, Limit: uint32(limit)}
		if source != nil {
			query.SourceType = *source
		}
		if state != nil {
			query.DesiredState = *state
		}
		return h.svc.ListConsumers(ctx, query)
	}()
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
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}
	items := make([]gin.H, 0, len(consumers))
	for _, consumer := range consumers {
		items = append(items, gin.H{
			"id":                 consumer.ID.String(),
			"workspace_id":       consumer.WorkspaceID.String(),
			"code":               consumer.Code,
			"name":               consumer.Name,
			"source_type":        consumer.SourceType,
			"broker_resource_id": consumer.BrokerResourceID.String(),
			"source_configured":  len(consumer.SourceConfigEnvelope) > 0,
			"topic":              consumer.Topic,
			"consumer_group":     consumer.ConsumerGroup,
			"mapping":            consumer.MappingJSON,
			"template_id":        consumer.TemplateID,
			"template_version":   consumer.TemplateVersion,
			"sender_profile_id":  consumer.SenderProfileID,
			"sender_version":     consumer.SenderVersion,
			"desired_state":      consumer.DesiredState,
			"parallelism":        consumer.Parallelism,
			"config_version":     consumer.ConfigVersion,
			"config_sha256":      hex.EncodeToString(consumer.ConfigSHA256),
			"created_at":         consumer.CreatedAt,
			"updated_at":         consumer.UpdatedAt,
		})
	}
	nextCursor := ""
	if len(consumers) == int(limit) {
		nextCursor = consumers[len(consumers)-1].ID.String()
	}
	apires.RespondSuccess(c, gin.H{"items": items, "next_cursor": nextCursor}, "mail consumers loaded")
}

func (h *TenantConsumerHandler) Update(c *gin.Context) {
	const op = "mail.tenant.consumer.update"
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

	consumerID, err := uuid.Parse(strings.TrimSpace(c.Param("id")))
	if err != nil {
		apires.RespondBadRequest(c, "invalid consumer id")
		return
	}
	var req mailReq.UpdateConsumerRequest
	// [COMMENT]: Inline bind JSON request body với maxBytes limit và strict DisallowUnknownFields check
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10)
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

	brokerID, err := uuid.Parse(strings.TrimSpace(req.BrokerResourceID))
	if err != nil {
		apires.RespondBadRequest(c, "invalid broker_resource_id")
		return
	}
	sourceConfigEnvelope, err := base64.StdEncoding.DecodeString(strings.TrimSpace(req.SourceConfigEnvelope))
	if err != nil || len(sourceConfigEnvelope) > 16<<10 {
		apires.RespondBadRequest(c, "invalid source_config_envelope")
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Topic = strings.TrimSpace(req.Topic)
	req.ConsumerGroup = strings.TrimSpace(req.ConsumerGroup)
	req.TemplateID = strings.TrimSpace(req.TemplateID)
	req.SenderProfileID = strings.TrimSpace(req.SenderProfileID)
	req.Mapping.RecipientJSONPath = strings.TrimSpace(req.Mapping.RecipientJSONPath)
	req.Mapping.ExternalMessageIDJSONPath = strings.TrimSpace(req.Mapping.ExternalMessageIDJSONPath)

	if req.Name == "" || len(req.Name) > 255 || req.Topic == "" || len(req.Topic) > 249 ||
		req.ConsumerGroup == "" || len(req.ConsumerGroup) > 255 ||
		req.TemplateID == "" || len(req.TemplateID) > 128 || req.SenderProfileID == "" || len(req.SenderProfileID) > 128 ||
		req.ExpectedConfigVersion == 0 {
		apires.RespondBadRequest(c, "invalid consumer update parameters")
		return
	}
	// [COMMENT]: Mapping syntax thuộc HTTP input contract, không đẩy validation xuống service/repository.
	if !strings.HasPrefix(req.Mapping.RecipientJSONPath, "$") || len(req.Mapping.RecipientJSONPath) > 512 ||
		(req.Mapping.ExternalMessageIDJSONPath != "" && (!strings.HasPrefix(req.Mapping.ExternalMessageIDJSONPath, "$") || len(req.Mapping.ExternalMessageIDJSONPath) > 512)) {
		apires.RespondBadRequest(c, "invalid consumer mapping")
		return
	}

	consumer, err := h.svc.UpdateConsumer(ctx, &mailEntity.TenantConsumer{ActorUserID: actorID, TenantID: tenantID, WorkspaceID: workspaceID, ZoneID: zoneID,
		ID: consumerID, ExpectedConfigVersion: req.ExpectedConfigVersion, Name: req.Name,
		SourceType: req.SourceType, BrokerResourceID: brokerID,
		SourceConfigEnvelope: sourceConfigEnvelope,
		Topic:                req.Topic, ConsumerGroup: req.ConsumerGroup,
		Mapping:    mailEntity.MessageMapping{ExternalMessageIDJSONPath: req.Mapping.ExternalMessageIDJSONPath, RecipientJSONPath: req.Mapping.RecipientJSONPath, VariableJSONPaths: req.Mapping.VariableJSONPaths},
		TemplateID: req.TemplateID, TemplateVersion: req.TemplateVersion,
		SenderProfileID: req.SenderProfileID, SenderVersion: req.SenderVersion,
		DesiredState: req.DesiredState, Parallelism: req.Parallelism,
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
		case errors.Is(err, mailTaxonomy.ErrVersionConflict):
			apires.RespondConflict(c, "resource version changed; reload before retrying")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{
		"id":                 consumer.ID.String(),
		"workspace_id":       consumer.WorkspaceID.String(),
		"code":               consumer.Code,
		"name":               consumer.Name,
		"source_type":        consumer.SourceType,
		"broker_resource_id": consumer.BrokerResourceID.String(),
		"source_configured":  len(consumer.SourceConfigEnvelope) > 0,
		"topic":              consumer.Topic,
		"consumer_group":     consumer.ConsumerGroup,
		"mapping":            consumer.MappingJSON,
		"template_id":        consumer.TemplateID,
		"template_version":   consumer.TemplateVersion,
		"sender_profile_id":  consumer.SenderProfileID,
		"sender_version":     consumer.SenderVersion,
		"desired_state":      consumer.DesiredState,
		"parallelism":        consumer.Parallelism,
		"config_version":     consumer.ConfigVersion,
		"config_sha256":      hex.EncodeToString(consumer.ConfigSHA256),
		"created_at":         consumer.CreatedAt,
		"updated_at":         consumer.UpdatedAt,
	}, "mail consumer updated")
}

func (h *TenantConsumerHandler) Pause(c *gin.Context)  { h.changeState(c, mailEntity.ConsumerPaused) }
func (h *TenantConsumerHandler) Resume(c *gin.Context) { h.changeState(c, mailEntity.ConsumerEnabled) }

func (h *TenantConsumerHandler) changeState(c *gin.Context, desiredState mailEntity.ConsumerDesiredState) {
	op := "mail.tenant.consumer." + string(desiredState)
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
	consumerID, err := uuid.Parse(strings.TrimSpace(c.Param("id")))
	if err != nil {
		apires.RespondBadRequest(c, "invalid consumer id")
		return
	}
	var req mailReq.ChangeConsumerStateRequest
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

	if req.ExpectedConfigVersion == 0 {
		apires.RespondBadRequest(c, "expected_config_version is required")
		return
	}
	consumer, err := h.svc.ChangeConsumerState(ctx, &mailEntity.TenantConsumer{ActorUserID: actorID, TenantID: tenantID, WorkspaceID: workspaceID, ZoneID: zoneID, ID: consumerID, ExpectedConfigVersion: req.ExpectedConfigVersion, DesiredState: desiredState})
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
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{
		"id":                 consumer.ID.String(),
		"workspace_id":       consumer.WorkspaceID.String(),
		"code":               consumer.Code,
		"name":               consumer.Name,
		"source_type":        consumer.SourceType,
		"broker_resource_id": consumer.BrokerResourceID.String(),
		"source_configured":  len(consumer.SourceConfigEnvelope) > 0,
		"topic":              consumer.Topic,
		"consumer_group":     consumer.ConsumerGroup,
		"mapping":            consumer.MappingJSON,
		"template_id":        consumer.TemplateID,
		"template_version":   consumer.TemplateVersion,
		"sender_profile_id":  consumer.SenderProfileID,
		"sender_version":     consumer.SenderVersion,
		"desired_state":      consumer.DesiredState,
		"parallelism":        consumer.Parallelism,
		"config_version":     consumer.ConfigVersion,
		"config_sha256":      hex.EncodeToString(consumer.ConfigSHA256),
		"created_at":         consumer.CreatedAt,
		"updated_at":         consumer.UpdatedAt,
	}, "mail consumer desired state updated")
}

func (h *TenantConsumerHandler) Delete(c *gin.Context) {
	const op = "mail.tenant.consumer.delete"
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
	consumerID, err := uuid.Parse(strings.TrimSpace(c.Param("id")))
	if err != nil {
		apires.RespondBadRequest(c, "invalid consumer id")
		return
	}
	var req mailReq.DeleteConsumerRequest
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

	req.Reason = strings.TrimSpace(req.Reason)
	if req.ExpectedConfigVersion == 0 || req.DrainTimeoutSeconds == 0 || req.DrainTimeoutSeconds > 3600 || len(req.Reason) > 512 {
		apires.RespondBadRequest(c, "invalid delete parameters")
		return
	}

	err = h.svc.DeleteConsumer(ctx, &mailEntity.TenantConsumer{ActorUserID: actorID, TenantID: tenantID, WorkspaceID: workspaceID, ZoneID: zoneID, ID: consumerID, ExpectedConfigVersion: req.ExpectedConfigVersion, DrainTimeoutSeconds: req.DrainTimeoutSeconds, Reason: req.Reason})
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
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "internal_error")
		}
		return
	}
	apires.RespondAccepted(c, gin.H{"consumer_id": consumerID.String(), "desired_state": mailEntity.ConsumerDeleting}, "mail consumer deletion scheduled")
}
