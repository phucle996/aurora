package mailSvcImpl

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
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

var tenantMailTemplateEventNamespace = uuid.MustParse("92712973-d86b-5e59-9a86-9bf5726c9981")

type tenantTemplateServiceImpl struct {
	repo mailRepoInterface.TenantTemplateRepository
}

func NewTenantTemplateService(repo mailRepoInterface.TenantTemplateRepository) mailSvcInterface.TenantTemplateService {
	return &tenantTemplateServiceImpl{repo: repo}
}

func (s *tenantTemplateServiceImpl) CreateTemplate(ctx context.Context, command *mailEntity.TenantTemplate) (*mailEntity.TenantTemplate, error) {
	// [COMMENT]: Tenant audit actor được giữ; template payload chỉ gồm subject + HTML và DP tự detect placeholder.
	canonicalContent, err := json.Marshal(struct {
		Subject string `json:"subject"`
		HTML    string `json:"html"`
	}{command.SubjectTemplate, command.HTMLTemplate})
	if err != nil {
		return nil, fmt.Errorf("mail tenant template service: canonicalize content: %w", err)
	}
	now, actor := time.Now().UTC(), command.ActorUserID
	templateID := uuid.New().String()
	contentHash := sha256.Sum256(canonicalContent)
	template := &mailEntity.TenantTemplate{
		ActorUserID: actor, TenantID: command.TenantID, ZoneID: command.ZoneID, ID: templateID, WorkspaceID: command.WorkspaceID,
		Code: command.Code, Name: command.Name, CurrentVersion: 1, TemplateRevision: 1, NextVersion: 2, NextRevision: 2,
		CreatedBy: &actor, UpdatedBy: &actor, CreatedAt: now, UpdatedAt: now,
		TemplateID: templateID, Version: 1, SubjectTemplate: command.SubjectTemplate, HTMLTemplate: command.HTMLTemplate,
		ContentSHA256: append([]byte(nil), contentHash[:]...), VersionCreatedBy: &actor, VersionCreatedAt: now,
	}
	eventID := uuid.NewSHA1(tenantMailTemplateEventNamespace, fmt.Appendf(nil, "template:%s:1:publish:%s", templateID, command.ZoneID))
	event := &mailproto.MailTemplateVersionPublishedV1{Metadata: &mailproto.MailEventMetadataV1{EventId: eventID[:], SchemaVersion: 1, OccurredAtUnixMs: now.UnixMilli(), Producer: "controlplane-mail"}, TemplateId: templateID, TemplateRevision: 1, TemplateVersion: 1, SubjectTemplate: template.SubjectTemplate, HtmlTemplate: template.HTMLTemplate, ContentSha256: template.ContentSHA256}
	var traceID []byte
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		event.Metadata.Traceparent = "00-" + spanContext.TraceID().String() + "-" + spanContext.SpanID().String() + "-" + fmt.Sprintf("%02x", byte(spanContext.TraceFlags()))
		id := spanContext.TraceID()
		traceID = append([]byte(nil), id[:]...)
	}
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("mail tenant template service: marshal create event: %w", err)
	}
	outbox := &mailEntity.MailOutboxRecord{EventID: eventID, ZoneID: command.ZoneID, JobTopic: "mail.template.version_published", Payload: payload, ActorUserID: &actor, Status: mailEntity.OutboxStatusPending, JobVersion: 1, ResourceID: templateID, PayloadSchemaVersion: 1, TraceID: traceID, Idle: 60}
	if err = s.repo.Create(ctx, template, outbox); err != nil {
		return nil, err
	}
	created, err := s.repo.GetByID(ctx, template)
	if err != nil {
		return nil, err
	}
	created.OperationID = outbox.EventID
	return created, nil
}

func (s *tenantTemplateServiceImpl) GetTemplate(ctx context.Context, command *mailEntity.TenantTemplate) (*mailEntity.TenantTemplate, error) {
	return s.repo.GetByID(ctx, command)
}
func (s *tenantTemplateServiceImpl) ListTemplates(ctx context.Context, command *mailEntity.TenantTemplate) ([]*mailEntity.TenantTemplate, error) {
	return s.repo.List(ctx, command)
}
func (s *tenantTemplateServiceImpl) ListTemplateVersions(ctx context.Context, command *mailEntity.TenantTemplate) ([]*mailEntity.TenantTemplate, error) {
	return s.repo.ListVersions(ctx, command)
}

func (s *tenantTemplateServiceImpl) PublishTemplateVersion(ctx context.Context, command *mailEntity.TenantTemplate) (*mailEntity.TenantTemplate, error) {
	command.ID = command.TemplateID
	template, err := s.repo.GetByID(ctx, command)
	if err != nil {
		return nil, err
	}
	if template.TemplateRevision != command.ExpectedRevision {
		return nil, mailTaxonomy.ErrVersionConflict
	}
	canonicalContent, err := json.Marshal(struct {
		Subject string `json:"subject"`
		HTML    string `json:"html"`
	}{command.SubjectTemplate, command.HTMLTemplate})
	if err != nil {
		return nil, fmt.Errorf("mail tenant template service: canonicalize publish content: %w", err)
	}
	now, actor := time.Now().UTC(), command.ActorUserID
	hash := sha256.Sum256(canonicalContent)
	template.ActorUserID, template.TenantID, template.ZoneID, template.ExpectedRevision = actor, command.TenantID, command.ZoneID, command.ExpectedRevision
	// [COMMENT]: Publish tạo candidate monotonic; current head chỉ được JO promote sau Zone ACK.
	template.UpdatedAt, template.UpdatedBy = now, &actor
	template.TemplateID, template.Version, template.TemplateRevision = template.ID, template.NextVersion, template.NextRevision
	template.SubjectTemplate, template.HTMLTemplate = command.SubjectTemplate, command.HTMLTemplate
	template.ContentSHA256, template.VersionCreatedBy, template.VersionCreatedAt = append([]byte(nil), hash[:]...), &actor, now
	eventID := uuid.NewSHA1(tenantMailTemplateEventNamespace, fmt.Appendf(nil, "template:%s:%d:publish:%s", template.ID, template.TemplateRevision, command.ZoneID))
	event := &mailproto.MailTemplateVersionPublishedV1{Metadata: &mailproto.MailEventMetadataV1{EventId: eventID[:], SchemaVersion: 1, OccurredAtUnixMs: now.UnixMilli(), Producer: "controlplane-mail"}, TemplateId: template.ID, TemplateRevision: template.TemplateRevision, TemplateVersion: template.Version, SubjectTemplate: template.SubjectTemplate, HtmlTemplate: template.HTMLTemplate, ContentSha256: template.ContentSHA256}
	var traceID []byte
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		event.Metadata.Traceparent = "00-" + spanContext.TraceID().String() + "-" + spanContext.SpanID().String() + "-" + fmt.Sprintf("%02x", byte(spanContext.TraceFlags()))
		id := spanContext.TraceID()
		traceID = append([]byte(nil), id[:]...)
	}
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("mail tenant template service: marshal publish event: %w", err)
	}
	outbox := &mailEntity.MailOutboxRecord{EventID: eventID, ZoneID: command.ZoneID, JobTopic: "mail.template.version_published", Payload: payload, ActorUserID: &actor, Status: mailEntity.OutboxStatusPending, JobVersion: 1, ResourceID: template.ID, PayloadSchemaVersion: 1, TraceID: traceID, Idle: 60}
	if err = s.repo.PublishVersion(ctx, template, outbox); err != nil {
		return nil, err
	}
	template.OperationID = outbox.EventID
	return template, nil
}

func (s *tenantTemplateServiceImpl) DeleteTemplate(ctx context.Context, command *mailEntity.TenantTemplate) error {
	command.ID = command.TemplateID
	template, err := s.repo.GetByID(ctx, command)
	if err != nil {
		return err
	}
	if template.TemplateRevision != command.ExpectedRevision {
		return mailTaxonomy.ErrVersionConflict
	}
	now, actor := time.Now().UTC(), command.ActorUserID
	// [COMMENT]: Delete retry giữ nguyên revision fence nhưng phải có operation ID mới sau một terminal failure.
	eventID := uuid.New()
	// [COMMENT]: Tombstone dùng next allocator để vượt cả revision candidate FAILED có thể từng đến Zone.
	event := &mailproto.MailTemplateDeletedV1{Metadata: &mailproto.MailEventMetadataV1{EventId: eventID[:], SchemaVersion: 1, OccurredAtUnixMs: now.UnixMilli(), Producer: "controlplane-mail"}, TemplateId: template.ID, TemplateRevision: template.NextRevision, LastPublishedVersion: template.CurrentVersion}
	var traceID []byte
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		event.Metadata.Traceparent = "00-" + spanContext.TraceID().String() + "-" + spanContext.SpanID().String() + "-" + fmt.Sprintf("%02x", byte(spanContext.TraceFlags()))
		id := spanContext.TraceID()
		traceID = append([]byte(nil), id[:]...)
	}
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(event)
	if err != nil {
		return fmt.Errorf("mail tenant template service: marshal delete event: %w", err)
	}
	outbox := &mailEntity.MailOutboxRecord{EventID: eventID, ZoneID: command.ZoneID, JobTopic: "mail.template.deleted", Payload: payload, ActorUserID: &actor, Status: mailEntity.OutboxStatusPending, JobVersion: 1, ResourceID: template.ID, PayloadSchemaVersion: 1, TraceID: traceID, Idle: 60}
	// [COMMENT]: Repository chỉ ghi delete outbox; JO xóa aggregate sau Zone ACK.
	command.ID, command.CurrentVersion, command.UpdatedAt, command.UpdatedBy = command.TemplateID, template.CurrentVersion, now, &actor
	if err = s.repo.Delete(ctx, command, outbox); err != nil {
		return err
	}
	command.OperationID = outbox.EventID
	return nil
}
