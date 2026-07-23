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
	"github.com/klauspost/compress/zstd"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

var personalMailTemplateEventNamespace = uuid.MustParse("9314352a-19ba-5808-b8e2-14e06df7b791")
var zstdEncoder, _ = zstd.NewWriter(nil)

type personalTemplateServiceImpl struct {
	repo mailRepoInterface.PersonalTemplateRepository
}

func NewPersonalTemplateService(repo mailRepoInterface.PersonalTemplateRepository) mailSvcInterface.PersonalTemplateService {
	return &personalTemplateServiceImpl{repo: repo}
}

// [COMMENT]: Service sở hữu hoàn toàn domain transition: tính SHA-256 canonical, nén zstd, tạo eventID và outbox record.
// Service tạo xong outbox entity rồi truyền 2 entity riêng (req, outbox) xuống repo.
func (s *personalTemplateServiceImpl) CreateTemplate(ctx context.Context, req *mailEntity.CreatePersonalTemplateRequest) (*mailEntity.CreatePersonalTemplateResponse, error) {
	rawHTML := req.RawHTML
	hasher := sha256.New()
	hasher.Write([]byte(req.SubjectTemplate))
	hasher.Write([]byte{0x00})
	hasher.Write([]byte(rawHTML))
	contentHash := hasher.Sum(nil)

	compressedHTML := zstdEncoder.EncodeAll([]byte(rawHTML), make([]byte, 0, len(rawHTML)))

	now := time.Now().UTC()
	templateID := uuid.New().String()

	eventID := uuid.NewSHA1(personalMailTemplateEventNamespace, fmt.Appendf(nil, "template:%s:1:publish:%s", templateID, req.ZoneID))
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
		return nil, fmt.Errorf("personal template service: marshal create event: %w", err)
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

	// [COMMENT]: Repo nhận thêm 2 staging fields: compressedHTML và contentHash.
	// Chúng không thuộc req entity — được inject vào CTE thông qua tham số riêng của repo method.
	// Tạm thời dùng closure struct để truyền xuống cho đến khi refactor repo signature hoàn chỉnh.
	return s.repo.Create(ctx, req, outbox, templateID, compressedHTML, contentHash)
}

func (s *personalTemplateServiceImpl) GetTemplate(ctx context.Context, req *mailEntity.GetPersonalTemplateRequest) (*mailEntity.GetPersonalTemplateResponse, error) {
	return s.repo.GetByID(ctx, req)
}

func (s *personalTemplateServiceImpl) ListTemplates(ctx context.Context, req *mailEntity.ListPersonalTemplatesRequest) ([]*mailEntity.PersonalTemplateItem, error) {
	return s.repo.List(ctx, req)
}

func (s *personalTemplateServiceImpl) ListTemplateVersions(ctx context.Context, req *mailEntity.ListPersonalTemplateVersionsRequest) ([]*mailEntity.PersonalTemplateVersionItem, error) {
	return s.repo.ListVersions(ctx, req)
}

// [COMMENT]: Service sở hữu domain transition luồng publish: SHA-256, nén zstd, tạo outbox skeleton.
// Outbox eventID và payload proto được repo finalize sau khi biết nextRevision (sau lock FOR UPDATE).
func (s *personalTemplateServiceImpl) PublishTemplateVersion(ctx context.Context, req *mailEntity.PublishPersonalTemplateVersionRequest) (*mailEntity.PublishPersonalTemplateVersionResponse, error) {
	rawHTML := req.RawHTML
	hasher := sha256.New()
	hasher.Write([]byte(req.SubjectTemplate))
	hasher.Write([]byte{0x00})
	hasher.Write([]byte(rawHTML))
	contentHash := hasher.Sum(nil)

	compressedHTML := zstdEncoder.EncodeAll([]byte(rawHTML), make([]byte, 0, len(rawHTML)))

	// [COMMENT]: Outbox skeleton — EventID và Payload sẽ được repo finalize với nextRevision đúng sau lock.
	// TraceID extract tại service vì ctx chỉ valid ở đây, không truyền ctx xuống repo để tránh side effect.
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

// [COMMENT]: Service sở hữu domain transition luồng delete: tạo eventID, outbox skeleton.
// Payload proto được repo finalize với nextRevision đúng sau khi lock và đọc revision.
func (s *personalTemplateServiceImpl) DeleteTemplate(ctx context.Context, req *mailEntity.DeletePersonalTemplateRequest) (uuid.UUID, error) {
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
