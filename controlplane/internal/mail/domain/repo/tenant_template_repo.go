package mailRepoInterface

import (
	"context"

	mailEntity "controlplane/internal/mail/domain/entity"
	"github.com/google/uuid"
)

// [COMMENT]: TenantTemplateRepository chỉ có thẩm quyền về: authorization (JOIN workspace + membership), locking, atomic aggregate + outbox CTE.
// Service truyền 2 entity riêng biệt cho các operation có outbox: req thuần túy và outbox đã được chuẩn bị.
// compressedHTML và contentHash là staging values do service tính — truyền riêng, không embed vào req.
type TenantTemplateRepository interface {
	// Create tạo mới Tenant template và insert outbox record trong 1 CTE nguyên tử.
	// templateID: UUID đã được service sinh; compressedHTML/contentHash: staging từ service.
	Create(ctx context.Context, req *mailEntity.CreateTenantTemplateRequest, outbox *mailEntity.MailOutboxRecord, templateID string, compressedHTML []byte, contentHash []byte) (*mailEntity.CreateTenantTemplateResponse, error)
	GetByID(ctx context.Context, req *mailEntity.GetTenantTemplateRequest) (*mailEntity.GetTenantTemplateResponse, error)
	List(ctx context.Context, req *mailEntity.ListTenantTemplatesRequest) ([]*mailEntity.TenantTemplateItem, error)
	ListVersions(ctx context.Context, req *mailEntity.ListTenantTemplateVersionsRequest) ([]*mailEntity.TenantTemplateVersionItem, error)
	// PublishVersion phát hành phiên bản mới; outbox được service chuẩn bị, repo finalize payload sau khi biết nextRevision.
	// compressedHTML/contentHash: staging từ service, cần để INSERT vào version table và proto payload.
	PublishVersion(ctx context.Context, req *mailEntity.PublishTenantTemplateVersionRequest, outbox *mailEntity.MailOutboxRecord, compressedHTML []byte, contentHash []byte) (*mailEntity.PublishTenantTemplateVersionResponse, error)
	// Delete xóa Tenant template bằng tombstone outbox; repo finalize payload proto sau khi lock và đọc nextRevision.
	Delete(ctx context.Context, req *mailEntity.DeleteTenantTemplateRequest, outbox *mailEntity.MailOutboxRecord) (uuid.UUID, error)
}
