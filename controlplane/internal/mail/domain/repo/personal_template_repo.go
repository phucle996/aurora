package mailRepoInterface

import (
	"context"

	mailEntity "controlplane/internal/mail/domain/entity"
	"github.com/google/uuid"
)

// [COMMENT]: PersonalTemplateRepository chỉ có thẩm quyền về: authorization, locking, atomic aggregate + outbox CTE.
// Service truyền 2 entity riêng biệt cho các operation có outbox: req thuần túy và outbox đã được chuẩn bị.
// compressedHTML và contentHash là staging values do service tính — truyền riêng, không embed vào req.
type PersonalTemplateRepository interface {
	// Create tạo mới template và insert outbox record trong 1 CTE nguyên tử.
	// templateID: UUID đã được service sinh; compressedHTML/contentHash: staging từ service.
	Create(ctx context.Context, req *mailEntity.CreatePersonalTemplateRequest, outbox *mailEntity.MailOutboxRecord, templateID string, compressedHTML []byte, contentHash []byte) (*mailEntity.CreatePersonalTemplateResponse, error)
	GetByID(ctx context.Context, req *mailEntity.GetPersonalTemplateRequest) (*mailEntity.GetPersonalTemplateResponse, error)
	List(ctx context.Context, req *mailEntity.ListPersonalTemplatesRequest) ([]*mailEntity.PersonalTemplateItem, error)
	ListVersions(ctx context.Context, req *mailEntity.ListPersonalTemplateVersionsRequest) ([]*mailEntity.PersonalTemplateVersionItem, error)
	// PublishVersion phát hành phiên bản mới; outbox được service chuẩn bị, repo finalize payload sau khi biết nextRevision.
	// compressedHTML/contentHash: staging từ service, cần để INSERT vào version table và proto payload.
	PublishVersion(ctx context.Context, req *mailEntity.PublishPersonalTemplateVersionRequest, outbox *mailEntity.MailOutboxRecord, compressedHTML []byte, contentHash []byte) (*mailEntity.PublishPersonalTemplateVersionResponse, error)
	// Delete xóa template bằng tombstone outbox; repo finalize payload proto sau khi lock và đọc nextRevision.
	Delete(ctx context.Context, req *mailEntity.DeletePersonalTemplateRequest, outbox *mailEntity.MailOutboxRecord) (uuid.UUID, error)
}
