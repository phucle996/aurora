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

var personalMailTemplateEventNamespace = uuid.MustParse("9314352a-19ba-5808-b8e2-14e06df7b791")

type personalTemplateServiceImpl struct {
	repo mailRepoInterface.PersonalTemplateRepository
}

func NewPersonalTemplateService(repo mailRepoInterface.PersonalTemplateRepository) mailSvcInterface.PersonalTemplateService {
	return &personalTemplateServiceImpl{repo: repo}
}

func (s *personalTemplateServiceImpl) CreateTemplate(ctx context.Context, command *mailEntity.PersonalTemplate) (*mailEntity.PersonalTemplate, error) {
	// [COMMENT]: Dataplane tự discover {{placeholder}} khi render; CP chỉ canonicalize content để tạo integrity hash.
	canonicalContent, err := json.Marshal(struct {
		Subject string `json:"subject"`
		HTML    string `json:"html"`
	}{command.SubjectTemplate, command.HTMLTemplate})
	if err != nil {
		return nil, fmt.Errorf("mail personal template service: canonicalize content: %w", err)
	}
	now := time.Now().UTC()
	templateID := uuid.New().String()
	contentHash := sha256.Sum256(canonicalContent)
	template := &mailEntity.PersonalTemplate{
		ActorUserID: command.ActorUserID, ZoneID: command.ZoneID, ID: templateID, WorkspaceID: command.WorkspaceID,
		Code: command.Code, Name: command.Name, CurrentVersion: 1, TemplateRevision: 1, NextVersion: 2, NextRevision: 2,
		CreatedAt: now, UpdatedAt: now, TemplateID: templateID, Version: 1,
		SubjectTemplate: command.SubjectTemplate, HTMLTemplate: command.HTMLTemplate,
		ContentSHA256: append([]byte(nil), contentHash[:]...), VersionCreatedAt: now,
	}
	outbox, err := personalTemplatePublishedOutbox(ctx, command.ActorUserID, command.ZoneID, template, now)
	if err != nil {
		return nil, err
	}
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
		return nil, fmt.Errorf("mail personal template service: canonicalize publish content: %w", err)
	}
	now := time.Now().UTC()
	hash := sha256.Sum256(canonicalContent)
	template.ActorUserID, template.ZoneID, template.ExpectedRevision = command.ActorUserID, command.ZoneID, command.ExpectedRevision
	// [COMMENT]: Publish tạo candidate monotonic; current head chỉ được JO promote sau Zone ACK.
	template.ExpectedRevision, template.UpdatedAt = command.ExpectedRevision, now
	template.TemplateID, template.Version, template.TemplateRevision = template.ID, template.NextVersion, template.NextRevision
	template.SubjectTemplate, template.HTMLTemplate = command.SubjectTemplate, command.HTMLTemplate
	template.ContentSHA256, template.VersionCreatedAt = append([]byte(nil), hash[:]...), now
	outbox, err := personalTemplatePublishedOutbox(ctx, command.ActorUserID, command.ZoneID, template, now)
	if err != nil {
		return nil, err
	}
	if err = s.repo.PublishVersion(ctx, template, outbox); err != nil {
		return nil, err
	}
	template.OperationID = outbox.EventID
	return template, nil
}

func (s *personalTemplateServiceImpl) DeleteTemplate(ctx context.Context, command *mailEntity.PersonalTemplate) error {
	command.ID = command.TemplateID
	template, err := s.repo.GetByID(ctx, command)
	if err != nil {
		return err
	}
	if template.TemplateRevision != command.ExpectedRevision {
		return mailTaxonomy.ErrVersionConflict
	}
	now := time.Now().UTC()
	// [COMMENT]: Delete retry giữ nguyên revision fence nhưng phải có operation ID mới sau một terminal failure.
	eventID := uuid.New()
	// [COMMENT]: Tombstone dùng next allocator để vượt cả revision candidate FAILED có thể từng đến Zone.
	event := &mailproto.MailTemplateDeletedV1{Metadata: &mailproto.MailEventMetadataV1{EventId: eventID[:], SchemaVersion: 1, OccurredAtUnixMs: now.UnixMilli(), Producer: "controlplane-mail"}, TemplateId: template.ID, TemplateRevision: template.NextRevision, LastPublishedVersion: template.CurrentVersion}
	traceID := attachPersonalTemplateTrace(ctx, event.Metadata)
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(event)
	if err != nil {
		return fmt.Errorf("mail personal template service: marshal delete event: %w", err)
	}
	actor := command.ActorUserID
	outbox := &mailEntity.MailOutboxRecord{EventID: eventID, ZoneID: command.ZoneID, JobTopic: "mail.template.deleted", Payload: payload, ActorUserID: &actor, Status: mailEntity.OutboxStatusPending, JobVersion: 1, ResourceID: template.ID, PayloadSchemaVersion: 1, TraceID: traceID, Idle: 60}
	// [COMMENT]: Repository chỉ ghi delete outbox; JO xóa aggregate sau Zone ACK.
	command.ID, command.CurrentVersion, command.UpdatedAt = command.TemplateID, template.CurrentVersion, now
	if err = s.repo.Delete(ctx, command, outbox); err != nil {
		return err
	}
	command.OperationID = outbox.EventID
	return nil
}

func personalTemplatePublishedOutbox(ctx context.Context, actor uuid.UUID, zone uuid.UUID, template *mailEntity.PersonalTemplate, now time.Time) (*mailEntity.MailOutboxRecord, error) {
	eventID := uuid.NewSHA1(personalMailTemplateEventNamespace, fmt.Appendf(nil, "template:%s:%d:publish:%s", template.ID, template.TemplateRevision, zone))
	event := &mailproto.MailTemplateVersionPublishedV1{Metadata: &mailproto.MailEventMetadataV1{EventId: eventID[:], SchemaVersion: 1, OccurredAtUnixMs: now.UnixMilli(), Producer: "controlplane-mail"}, TemplateId: template.ID, TemplateRevision: template.TemplateRevision, TemplateVersion: template.Version, SubjectTemplate: template.SubjectTemplate, HtmlTemplate: template.HTMLTemplate, ContentSha256: template.ContentSHA256}
	traceID := attachPersonalTemplateTrace(ctx, event.Metadata)
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("mail personal template service: marshal publish event: %w", err)
	}
	return &mailEntity.MailOutboxRecord{EventID: eventID, ZoneID: zone, JobTopic: "mail.template.version_published", Payload: payload, ActorUserID: &actor, Status: mailEntity.OutboxStatusPending, JobVersion: 1, ResourceID: template.ID, PayloadSchemaVersion: 1, TraceID: traceID, Idle: 60}, nil
}

func attachPersonalTemplateTrace(ctx context.Context, metadata *mailproto.MailEventMetadataV1) []byte {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return nil
	}
	metadata.Traceparent = "00-" + spanContext.TraceID().String() + "-" + spanContext.SpanID().String() + "-" + fmt.Sprintf("%02x", byte(spanContext.TraceFlags()))
	id := spanContext.TraceID()
	return append([]byte(nil), id[:]...)
}
