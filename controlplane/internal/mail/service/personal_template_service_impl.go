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
	// [COMMENT]: Dataplane tự discover {{placeholder}} khi render; CP chỉ canonicalize subject + HTML để hash/idempotency.
	canonicalContent, err := json.Marshal(struct {
		Subject string `json:"subject"`
		HTML    string `json:"html"`
	}{command.SubjectTemplate, command.HTMLTemplate})
	if err != nil {
		return nil, fmt.Errorf("mail personal template service: canonicalize content: %w", err)
	}
	now := time.Now().UTC()
	templateID := uuid.NewSHA1(personalMailTemplateEventNamespace, []byte("create:"+command.WorkspaceID.String()+":"+command.IdempotencyKey)).String()
	contentHash := sha256.Sum256(canonicalContent)
	requestCanonical, err := json.Marshal(struct {
		Name    string `json:"name"`
		Content []byte `json:"content"`
	}{command.Name, canonicalContent})
	if err != nil {
		return nil, fmt.Errorf("mail personal template service: canonicalize request: %w", err)
	}
	requestHash := sha256.Sum256(requestCanonical)
	template := &mailEntity.PersonalTemplate{
		ActorUserID: command.ActorUserID, ZoneID: command.ZoneID, ID: templateID, WorkspaceID: command.WorkspaceID,
		Name: command.Name, CurrentVersion: 1, TemplateRevision: 1, Status: mailEntity.TemplateActive,
		IdempotencyKey: command.IdempotencyKey, CreateRequestSHA256: append([]byte(nil), requestHash[:]...),
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
	template, err := s.repo.GetByID(ctx, command)
	if err != nil {
		return nil, err
	}
	if template.Status != mailEntity.TemplateActive {
		return nil, mailTaxonomy.ErrInvalidArgument
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
	template.CurrentVersion, template.TemplateRevision, template.UpdatedAt = template.CurrentVersion+1, template.TemplateRevision+1, now
	template.TemplateID, template.Version = template.ID, template.CurrentVersion
	template.SubjectTemplate, template.HTMLTemplate = command.SubjectTemplate, command.HTMLTemplate
	template.ContentSHA256, template.VersionCreatedAt = append([]byte(nil), hash[:]...), now
	outbox, err := personalTemplatePublishedOutbox(ctx, command.ActorUserID, command.ZoneID, template, now)
	if err != nil {
		return nil, err
	}
	if err = s.repo.PublishVersion(ctx, template, outbox); err != nil {
		return nil, err
	}
	return template, nil
}

func (s *personalTemplateServiceImpl) ArchiveTemplate(ctx context.Context, command *mailEntity.PersonalTemplate) error {
	command.ID = command.TemplateID
	template, err := s.repo.GetByID(ctx, command)
	if err != nil {
		return err
	}
	if template.Status != mailEntity.TemplateActive {
		return mailTaxonomy.ErrInvalidArgument
	}
	if template.TemplateRevision != command.ExpectedRevision {
		return mailTaxonomy.ErrVersionConflict
	}
	now := time.Now().UTC()
	eventID := uuid.NewSHA1(personalMailTemplateEventNamespace, fmt.Appendf(nil, "template:%s:%d:archive:%s", template.ID, command.ExpectedRevision+1, command.ZoneID))
	event := &mailproto.MailTemplateArchivedV1{Metadata: &mailproto.MailEventMetadataV1{EventId: eventID[:], SchemaVersion: 1, OccurredAtUnixMs: now.UnixMilli(), Producer: "controlplane-mail"}, TemplateId: template.ID, TemplateRevision: command.ExpectedRevision + 1, LastPublishedVersion: template.CurrentVersion}
	traceID := attachPersonalTemplateTrace(ctx, event.Metadata)
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(event)
	if err != nil {
		return fmt.Errorf("mail personal template service: marshal archive event: %w", err)
	}
	actor := command.ActorUserID
	outbox := &mailEntity.MailOutboxRecord{EventID: eventID, RoutingScope: "zone:" + command.ZoneID.String(), JobTopic: "mail.template.archived", Payload: payload, ActorUserID: &actor, Status: mailEntity.OutboxStatusPending, JobVersion: 1, ResourceID: template.ID, PayloadSchemaVersion: 1, TraceID: traceID, Idle: 60}
	command.ID, command.UpdatedAt = command.TemplateID, now
	return s.repo.Archive(ctx, command, outbox)
}

func personalTemplatePublishedOutbox(ctx context.Context, actor uuid.UUID, zone uuid.UUID, template *mailEntity.PersonalTemplate, now time.Time) (*mailEntity.MailOutboxRecord, error) {
	eventID := uuid.NewSHA1(personalMailTemplateEventNamespace, fmt.Appendf(nil, "template:%s:%d:publish:%s", template.ID, template.TemplateRevision, zone))
	event := &mailproto.MailTemplateVersionPublishedV1{Metadata: &mailproto.MailEventMetadataV1{EventId: eventID[:], SchemaVersion: 1, OccurredAtUnixMs: now.UnixMilli(), Producer: "controlplane-mail"}, TemplateId: template.ID, TemplateRevision: template.TemplateRevision, TemplateVersion: template.Version, SubjectTemplate: template.SubjectTemplate, HtmlTemplate: template.HTMLTemplate, ContentSha256: template.ContentSHA256}
	traceID := attachPersonalTemplateTrace(ctx, event.Metadata)
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("mail personal template service: marshal publish event: %w", err)
	}
	return &mailEntity.MailOutboxRecord{EventID: eventID, RoutingScope: "zone:" + zone.String(), JobTopic: "mail.template.version_published", Payload: payload, ActorUserID: &actor, Status: mailEntity.OutboxStatusPending, JobVersion: 1, ResourceID: template.ID, PayloadSchemaVersion: 1, TraceID: traceID, Idle: 60}, nil
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
