package mailSvcImpl

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoInterface "controlplane/internal/mail/domain/repo"
	mailSvcInterface "controlplane/internal/mail/domain/service"
	mailproto "controlplane/internal/mail/transport/rpc/proto"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

var tenantMailTemplateEventNamespace = uuid.MustParse("d287042a-44e2-5407-a3f1-28562cd9b722")

type tenantTemplateServiceImpl struct {
	repo mailRepoInterface.TenantTemplateRepository
}

func NewTenantTemplateService(repo mailRepoInterface.TenantTemplateRepository) mailSvcInterface.TenantTemplateService {
	return &tenantTemplateServiceImpl{repo: repo}
}

// [COMMENT]: Tenant Service sở hữu domain transition: tính SHA-256 canonical, nén zstd, tạo eventID và outbox record.
// Service tạo xong outbox entity rồi truyền 2 entity riêng (req, outbox) cùng staging values xuống repo.
func (s *tenantTemplateServiceImpl) CreateTemplate(ctx context.Context, req *mailEntity.CreateTenantTemplateRequest) (*mailEntity.CreateTenantTemplateResponse, error) {
	rawHTML := req.RawHTML
	hasher := sha256.New()
	hasher.Write([]byte(req.SubjectTemplate))
	hasher.Write([]byte{0x00})
	hasher.Write([]byte(rawHTML))
	contentHash := hasher.Sum(nil)

	compressedHTML := zstdEncoder.EncodeAll([]byte(rawHTML), make([]byte, 0, len(rawHTML)))

	now := time.Now().UTC()
	templateID := uuid.New().String()

	eventID := uuid.NewSHA1(tenantMailTemplateEventNamespace, fmt.Appendf(nil, "template:%s:1:publish:%s", templateID, req.ZoneID))
	event := &mailproto.MailTemplateVersionPublishedV1{
		Metadata:        &mailproto.MailEventMetadataV1{EventId: eventID[:], SchemaVersion: 1, OccurredAtUnixMs: now.UnixMilli(), Producer: "controlplane-mail"},
		TemplateId:      templateID, TemplateRevision: 1, TemplateVersion: 1,
		SubjectTemplate: req.SubjectTemplate, HtmlTemplate: compressedHTML, ContentSha256: contentHash,
	}
	var traceID []byte
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		event.Metadata.Traceparent = "00-" + spanContext.TraceID().String() + "-" + spanContext.SpanID().String() + "-" + fmt.Sprintf("%02x", byte(spanContext.TraceFlags()))
		id := spanContext.TraceID()
		traceID = append([]byte(nil), id[:]...)
	}
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("tenant template service: marshal create event: %w", err)
	}

	// [COMMENT]: outbox là entity riêng — không embed vào req. Service truyền 2 entity xuống repo.
	outbox := &mailEntity.MailOutboxRecord{
		EventID:              eventID,
		ZoneID:               req.ZoneID,
		JobTopic:             "mail.template.version_published",
		Payload:              payload,
		ActorUserID:          req.ActorUserID,
		Status:               mailEntity.OutboxStatusPending,
		JobVersion:           1,
		ResourceID:           templateID,
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 60,
	}

	return s.repo.Create(ctx, req, outbox, templateID, compressedHTML, contentHash)
}

func (s *tenantTemplateServiceImpl) GetTemplate(ctx context.Context, req *mailEntity.GetTenantTemplateRequest) (*mailEntity.GetTenantTemplateResponse, error) {
	return s.repo.GetByID(ctx, req)
}

func (s *tenantTemplateServiceImpl) ListTemplates(ctx context.Context, req *mailEntity.ListTenantTemplatesRequest) ([]*mailEntity.TenantTemplateItem, error) {
	return s.repo.List(ctx, req)
}

func (s *tenantTemplateServiceImpl) ListTemplateVersions(ctx context.Context, req *mailEntity.ListTenantTemplateVersionsRequest) ([]*mailEntity.TenantTemplateVersionItem, error) {
	return s.repo.ListVersions(ctx, req)
}

// [COMMENT]: Tenant Service sở hữu domain transition luồng publish: SHA-256, nén zstd, tạo outbox skeleton.
// Outbox eventID và payload proto được repo finalize sau khi biết nextRevision (sau lock FOR UPDATE).
func (s *tenantTemplateServiceImpl) PublishTemplateVersion(ctx context.Context, req *mailEntity.PublishTenantTemplateVersionRequest) (*mailEntity.PublishTenantTemplateVersionResponse, error) {
	rawHTML := req.RawHTML
	hasher := sha256.New()
	hasher.Write([]byte(req.SubjectTemplate))
	hasher.Write([]byte{0x00})
	hasher.Write([]byte(rawHTML))
	contentHash := hasher.Sum(nil)

	compressedHTML := zstdEncoder.EncodeAll([]byte(rawHTML), make([]byte, 0, len(rawHTML)))

	// [COMMENT]: TraceID extract tại service vì ctx chỉ valid ở đây.
	var traceID []byte
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		id := spanContext.TraceID()
		traceID = append([]byte(nil), id[:]...)
	}

	// [COMMENT]: outbox là entity riêng — không embed vào req. Service truyền 2 entity xuống repo.
	outbox := &mailEntity.MailOutboxRecord{
		ZoneID:               req.ZoneID,
		JobTopic:             "mail.template.version_published",
		ActorUserID:          req.ActorUserID,
		Status:               mailEntity.OutboxStatusPending,
		JobVersion:           1,
		ResourceID:           req.TemplateID,
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 60,
	}

	return s.repo.PublishVersion(ctx, req, outbox, compressedHTML, contentHash)
}

// [COMMENT]: Tenant Service sở hữu domain transition luồng delete: tạo eventID, outbox skeleton.
// Payload proto được repo finalize với nextRevision đúng sau khi lock và đọc revision.
func (s *tenantTemplateServiceImpl) DeleteTemplate(ctx context.Context, req *mailEntity.DeleteTenantTemplateRequest) (uuid.UUID, error) {
	eventID := uuid.New()

	var traceID []byte
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		id := spanContext.TraceID()
		traceID = append([]byte(nil), id[:]...)
	}

	// [COMMENT]: outbox là entity riêng — không embed vào req. Service truyền 2 entity xuống repo.
	outbox := &mailEntity.MailOutboxRecord{
		EventID:              eventID,
		ZoneID:               req.ZoneID,
		JobTopic:             "mail.template.deleted",
		ActorUserID:          req.ActorUserID,
		Status:               mailEntity.OutboxStatusPending,
		JobVersion:           1,
		ResourceID:           req.TemplateID,
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 60,
	}

	return s.repo.Delete(ctx, req, outbox)
}
