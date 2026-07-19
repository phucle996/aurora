package mailSvcImpl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoInterface "controlplane/internal/mail/domain/repo"
	mailSvcInterface "controlplane/internal/mail/domain/service"
	mailTaxonomy "controlplane/internal/mail/taxonomy"
	mailproto "controlplane/internal/mail/transport/rpc/proto"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

var personalMailTemplateEventNamespace = uuid.MustParse("9314352a-19ba-5808-b8e2-14e06df7b791")

type personalTemplateServiceImpl struct {
	repo mailRepoInterface.PersonalTemplateRepository
}

// NewPersonalTemplateService khoi tao service quan ly mail template o scope Personal
func NewPersonalTemplateService(repo mailRepoInterface.PersonalTemplateRepository) mailSvcInterface.PersonalTemplateService {
	return &personalTemplateServiceImpl{repo: repo}
}

func (s *personalTemplateServiceImpl) CreateTemplate(ctx context.Context, command *mailEntity.PersonalTemplate) (*mailEntity.PersonalTemplate, error) {
	// [COMMENT]: Handler đã normalize/validate template input; service canonicalize content để version/hash ổn định.
	// [COMMENT]: Personal flow tự canonicalize schema và chỉ chấp nhận {{variable.path}}, không gọi template helper dùng chung.
	schemaJSON := command.VariableSchemaJSON
	if len(bytes.TrimSpace(schemaJSON)) == 0 {
		schemaJSON = json.RawMessage(`{}`)
	}

	var schemaValue any
	decoder := json.NewDecoder(bytes.NewReader(schemaJSON))
	decoder.UseNumber()
	if err := decoder.Decode(&schemaValue); err != nil {
		return nil, mailTaxonomy.ErrTemplateSyntax
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, mailTaxonomy.ErrTemplateSyntax
	}

	schemaObject, ok := schemaValue.(map[string]any)
	if !ok || (schemaObject["type"] != nil && schemaObject["type"] != "object") {
		return nil, mailTaxonomy.ErrTemplateSyntax
	}

	properties := map[string]any{}
	if rawProperties, exists := schemaObject["properties"]; exists {
		properties, ok = rawProperties.(map[string]any)
		if !ok {
			return nil, mailTaxonomy.ErrTemplateSyntax
		}
	}

	// [COMMENT]: Kiem tra va validate placeholder syntax {{variable.path}} trong template body
	for _, body := range []string{command.SubjectTemplate, command.TextTemplate, command.HTMLTemplate} {
		remaining := body
		for {
			open := strings.Index(remaining, "{{")
			closeBeforeOpen := strings.Index(remaining, "}}")
			if closeBeforeOpen >= 0 && (open < 0 || closeBeforeOpen < open) {
				return nil, mailTaxonomy.ErrTemplateSyntax
			}
			if open < 0 {
				break
			}
			closeOffset := strings.Index(remaining[open+2:], "}}")
			if closeOffset < 0 {
				return nil, mailTaxonomy.ErrTemplateSyntax
			}
			close := open + 2 + closeOffset
			token := strings.TrimSpace(remaining[open+2 : close])
			if token == "" || len(token) > 128 {
				return nil, mailTaxonomy.ErrTemplateSyntax
			}
			for _, char := range token {
				if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '.' || char == '-') {
					return nil, mailTaxonomy.ErrTemplateSyntax
				}
			}
			if _, declared := properties[strings.SplitN(token, ".", 2)[0]]; !declared {
				return nil, mailTaxonomy.ErrTemplateSyntax
			}
			remaining = remaining[close+2:]
		}
	}

	canonicalSchema, err := json.Marshal(schemaValue)
	if err != nil {
		return nil, fmt.Errorf("mail personal template service: canonicalize schema: %w", err)
	}

	canonicalContent, err := json.Marshal(struct {
		Subject string          `json:"subject"`
		Text    string          `json:"text"`
		HTML    string          `json:"html"`
		Schema  json.RawMessage `json:"schema"`
	}{
		Subject: command.SubjectTemplate,
		Text:    command.TextTemplate,
		HTML:    command.HTMLTemplate,
		Schema:  canonicalSchema,
	})
	if err != nil {
		return nil, fmt.Errorf("mail personal template service: canonicalize content: %w", err)
	}

	now := time.Now().UTC()
	actor := command.ActorUserID
	templateID := uuid.NewSHA1(personalMailTemplateEventNamespace, []byte("create:"+command.WorkspaceID.String()+":"+command.IdempotencyKey)).String()
	contentHash := sha256.Sum256(canonicalContent)

	requestCanonical, err := json.Marshal(struct {
		Name    string `json:"name"`
		Content []byte `json:"content"`
	}{
		Name:    command.Name,
		Content: canonicalContent,
	})
	if err != nil {
		return nil, fmt.Errorf("mail personal template service: canonicalize request: %w", err)
	}
	requestHash := sha256.Sum256(requestCanonical)

	// [COMMENT]: Khoi tao PersonalTemplate entity
	template := &mailEntity.PersonalTemplate{
		ActorUserID:         command.ActorUserID,
		ZoneID:              command.ZoneID,
		ID:                  templateID,
		WorkspaceID:         command.WorkspaceID,
		Scope:               mailEntity.TemplateScopeWorkspace,
		Name:                command.Name,
		CurrentVersion:      1,
		TemplateRevision:    1,
		Status:              mailEntity.TemplateActive,
		IdempotencyKey:      command.IdempotencyKey,
		CreateRequestSHA256: append([]byte(nil), requestHash[:]...),
		CreatedBy:           &actor,
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	version := &mailEntity.PersonalTemplate{
		TemplateID:         templateID,
		Version:            1,
		SubjectTemplate:    command.SubjectTemplate,
		TextTemplate:       command.TextTemplate,
		HTMLTemplate:       command.HTMLTemplate,
		VariableSchemaJSON: canonicalSchema,
		ContentSHA256:      append([]byte(nil), contentHash[:]...),
		VersionCreatedBy:   &actor,
		VersionCreatedAt:   now,
	}

	eventID := uuid.NewSHA1(personalMailTemplateEventNamespace, []byte("template:"+templateID+":1:publish:"+command.ZoneID.String()))
	event := &mailproto.MailTemplateVersionPublishedV1{
		Metadata: &mailproto.MailEventMetadataV1{
			EventId:          eventID[:],
			SchemaVersion:    1,
			OccurredAtUnixMs: now.UnixMilli(),
			Producer:         "controlplane-mail",
		},
		TemplateId:         templateID,
		TemplateRevision:   1,
		TemplateVersion:    1,
		SubjectTemplate:    version.SubjectTemplate,
		TextTemplate:       version.TextTemplate,
		HtmlTemplate:       version.HTMLTemplate,
		VariableSchemaJson: version.VariableSchemaJSON,
		ContentSha256:      version.ContentSHA256,
	}

	var traceID []byte
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		event.Metadata.Traceparent = "00-" + spanContext.TraceID().String() + "-" + spanContext.SpanID().String() + "-" + fmt.Sprintf("%02x", byte(spanContext.TraceFlags()))
		id := spanContext.TraceID()
		traceID = append([]byte(nil), id[:]...)
	}

	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("mail personal template service: marshal create event: %w", err)
	}

	outbox := &mailEntity.MailOutboxRecord{
		EventID:              eventID,
		RoutingScope:         "zone:" + command.ZoneID.String(),
		JobTopic:             "mail.template.version_published",
		Payload:              payload,
		ActorUserID:          &actor,
		Status:               mailEntity.OutboxStatusPending,
		JobVersion:           1,
		ResourceID:           templateID,
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 60,
	}

	// [COMMENT]: Template, immutable version và outbox chỉ commit cùng nhau trong Personal CTE.
	template.TemplateID = version.TemplateID
	template.Version = version.Version
	template.SubjectTemplate = version.SubjectTemplate
	template.TextTemplate = version.TextTemplate
	template.HTMLTemplate = version.HTMLTemplate
	template.VariableSchemaJSON = version.VariableSchemaJSON
	template.ContentSHA256 = version.ContentSHA256
	template.VersionCreatedBy = version.VersionCreatedBy
	template.VersionCreatedAt = version.VersionCreatedAt

	if err = s.repo.Create(ctx, template, outbox); err != nil {
		return nil, err
	}
	return s.repo.GetByID(ctx, template)
}

func (s *personalTemplateServiceImpl) GetTemplate(ctx context.Context, command *mailEntity.PersonalTemplate) (*mailEntity.PersonalTemplate, error) {
	return s.repo.GetByID(ctx, command)
}

func (s *personalTemplateServiceImpl) ListTemplates(ctx context.Context, command *mailEntity.PersonalTemplate) ([]*mailEntity.PersonalTemplate, error) {
	return s.repo.List(ctx, command)
}

func (s *personalTemplateServiceImpl) ListTemplateVersions(ctx context.Context, command *mailEntity.PersonalTemplate) ([]*mailEntity.PersonalTemplate, error) {
	return s.repo.ListVersions(ctx, command)
}

func (s *personalTemplateServiceImpl) PublishTemplateVersion(ctx context.Context, command *mailEntity.PersonalTemplate) (*mailEntity.PersonalTemplate, error) {
	command.ID = command.TemplateID
	template, err := s.GetTemplate(ctx, command)
	if err != nil {
		return nil, err
	}

	if command.ExpectedRevision == 0 || template.Scope != mailEntity.TemplateScopeWorkspace || template.Status != mailEntity.TemplateActive {
		return nil, mailTaxonomy.ErrInvalidArgument
	}
	if template.TemplateRevision != command.ExpectedRevision {
		return nil, mailTaxonomy.ErrVersionConflict
	}

	// [COMMENT]: Handler đã kiểm tra kích thước và hình dạng input; service chỉ biên dịch nội dung template.
	schemaJSON := command.VariableSchemaJSON
	if len(bytes.TrimSpace(schemaJSON)) == 0 {
		schemaJSON = json.RawMessage(`{}`)
	}

	var schemaValue any
	decoder := json.NewDecoder(bytes.NewReader(schemaJSON))
	decoder.UseNumber()
	if decoder.Decode(&schemaValue) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, mailTaxonomy.ErrTemplateSyntax
	}

	schemaObject, ok := schemaValue.(map[string]any)
	if !ok || (schemaObject["type"] != nil && schemaObject["type"] != "object") {
		return nil, mailTaxonomy.ErrTemplateSyntax
	}

	properties := map[string]any{}
	if raw, exists := schemaObject["properties"]; exists {
		properties, ok = raw.(map[string]any)
		if !ok {
			return nil, mailTaxonomy.ErrTemplateSyntax
		}
	}

	for _, body := range []string{command.SubjectTemplate, command.TextTemplate, command.HTMLTemplate} {
		remaining := body
		for {
			open := strings.Index(remaining, "{{")
			earlyClose := strings.Index(remaining, "}}")
			if earlyClose >= 0 && (open < 0 || earlyClose < open) {
				return nil, mailTaxonomy.ErrTemplateSyntax
			}
			if open < 0 {
				break
			}
			offset := strings.Index(remaining[open+2:], "}}")
			if offset < 0 {
				return nil, mailTaxonomy.ErrTemplateSyntax
			}
			close := open + 2 + offset
			token := strings.TrimSpace(remaining[open+2 : close])
			if token == "" || len(token) > 128 {
				return nil, mailTaxonomy.ErrTemplateSyntax
			}
			for _, char := range token {
				if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '.' || char == '-') {
					return nil, mailTaxonomy.ErrTemplateSyntax
				}
			}
			if _, declared := properties[strings.SplitN(token, ".", 2)[0]]; !declared {
				return nil, mailTaxonomy.ErrTemplateSyntax
			}
			remaining = remaining[close+2:]
		}
	}

	canonicalSchema, err := json.Marshal(schemaValue)
	if err != nil {
		return nil, fmt.Errorf("mail personal template service: canonicalize publish schema: %w", err)
	}

	canonicalContent, err := json.Marshal(struct {
		Subject string          `json:"subject"`
		Text    string          `json:"text"`
		HTML    string          `json:"html"`
		Schema  json.RawMessage `json:"schema"`
	}{
		Subject: command.SubjectTemplate,
		Text:    command.TextTemplate,
		HTML:    command.HTMLTemplate,
		Schema:  canonicalSchema,
	})
	if err != nil {
		return nil, fmt.Errorf("mail personal template service: canonicalize publish content: %w", err)
	}

	now := time.Now().UTC()
	actor := command.ActorUserID
	hash := sha256.Sum256(canonicalContent)

	version := &mailEntity.PersonalTemplate{
		TemplateID:         template.ID,
		Version:            template.CurrentVersion + 1,
		SubjectTemplate:    command.SubjectTemplate,
		TextTemplate:       command.TextTemplate,
		HTMLTemplate:       command.HTMLTemplate,
		VariableSchemaJSON: canonicalSchema,
		ContentSHA256:      append([]byte(nil), hash[:]...),
		VersionCreatedBy:   &actor,
		VersionCreatedAt:   now,
	}

	template.CurrentVersion = version.Version
	template.TemplateRevision = template.TemplateRevision + 1
	template.UpdatedAt = now

	eventID := uuid.NewSHA1(personalMailTemplateEventNamespace, fmt.Appendf(nil, "template:%s:%d:publish:%s", template.ID, template.TemplateRevision, command.ZoneID))
	event := &mailproto.MailTemplateVersionPublishedV1{
		Metadata: &mailproto.MailEventMetadataV1{
			EventId:          eventID[:],
			SchemaVersion:    1,
			OccurredAtUnixMs: now.UnixMilli(),
			Producer:         "controlplane-mail",
		},
		TemplateId:         template.ID,
		TemplateRevision:   template.TemplateRevision,
		TemplateVersion:    version.Version,
		SubjectTemplate:    version.SubjectTemplate,
		TextTemplate:       version.TextTemplate,
		HtmlTemplate:       version.HTMLTemplate,
		VariableSchemaJson: version.VariableSchemaJSON,
		ContentSha256:      version.ContentSHA256,
	}

	var traceID []byte
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		event.Metadata.Traceparent = "00-" + spanContext.TraceID().String() + "-" + spanContext.SpanID().String() + "-" + fmt.Sprintf("%02x", byte(spanContext.TraceFlags()))
		id := spanContext.TraceID()
		traceID = append([]byte(nil), id[:]...)
	}

	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("mail personal template service: marshal publish event: %w", err)
	}

	outbox := &mailEntity.MailOutboxRecord{
		EventID:              eventID,
		RoutingScope:         "zone:" + command.ZoneID.String(),
		JobTopic:             "mail.template.version_published",
		Payload:              payload,
		ActorUserID:          &actor,
		Status:               mailEntity.OutboxStatusPending,
		JobVersion:           1,
		ResourceID:           template.ID,
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 60,
	}

	template.ActorUserID = command.ActorUserID
	template.ZoneID = command.ZoneID
	template.ExpectedRevision = command.ExpectedRevision
	template.TemplateID = version.TemplateID
	template.Version = version.Version
	template.SubjectTemplate = version.SubjectTemplate
	template.TextTemplate = version.TextTemplate
	template.HTMLTemplate = version.HTMLTemplate
	template.VariableSchemaJSON = version.VariableSchemaJSON
	template.ContentSHA256 = version.ContentSHA256
	template.VersionCreatedBy = version.VersionCreatedBy
	template.VersionCreatedAt = version.VersionCreatedAt

	if err = s.repo.PublishVersion(ctx, template, outbox); err != nil {
		return nil, err
	}
	return template, nil
}

func (s *personalTemplateServiceImpl) ArchiveTemplate(ctx context.Context, command *mailEntity.PersonalTemplate) error {
	command.ID = command.TemplateID
	template, err := s.GetTemplate(ctx, command)
	if err != nil {
		return err
	}

	if command.ExpectedRevision == 0 || template.Scope != mailEntity.TemplateScopeWorkspace || template.Status != mailEntity.TemplateActive {
		return mailTaxonomy.ErrInvalidArgument
	}
	if template.TemplateRevision != command.ExpectedRevision {
		return mailTaxonomy.ErrVersionConflict
	}

	now := time.Now().UTC()
	actor := command.ActorUserID

	eventID := uuid.NewSHA1(personalMailTemplateEventNamespace, fmt.Appendf(nil, "template:%s:%d:archive:%s", template.ID, command.ExpectedRevision+1, command.ZoneID))
	event := &mailproto.MailTemplateArchivedV1{
		Metadata: &mailproto.MailEventMetadataV1{
			EventId:          eventID[:],
			SchemaVersion:    1,
			OccurredAtUnixMs: now.UnixMilli(),
			Producer:         "controlplane-mail",
		},
		TemplateId:           template.ID,
		TemplateRevision:     command.ExpectedRevision + 1,
		LastPublishedVersion: template.CurrentVersion,
	}

	var traceID []byte
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		event.Metadata.Traceparent = "00-" + spanContext.TraceID().String() + "-" + spanContext.SpanID().String() + "-" + fmt.Sprintf("%02x", byte(spanContext.TraceFlags()))
		id := spanContext.TraceID()
		traceID = append([]byte(nil), id[:]...)
	}

	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(event)
	if err != nil {
		return fmt.Errorf("mail personal template service: marshal archive event: %w", err)
	}

	outbox := &mailEntity.MailOutboxRecord{
		EventID:              eventID,
		RoutingScope:         "zone:" + command.ZoneID.String(),
		JobTopic:             "mail.template.archived",
		Payload:              payload,
		ActorUserID:          &actor,
		Status:               mailEntity.OutboxStatusPending,
		JobVersion:           1,
		ResourceID:           template.ID,
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 60,
	}

	// [COMMENT]: Archive state và tombstone outbox commit atomically trong Personal CTE.
	command.ID = command.TemplateID
	command.UpdatedAt = now

	return s.repo.Archive(ctx, command, outbox)
}
