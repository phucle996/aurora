package mailRepoInterface

import (
	"context"
	mailEntity "controlplane/internal/mail/domain/entity"

	"github.com/google/uuid"
)

type EndpointRepository interface {
	// Create lưu trữ Endpoint vật lý mới và ghi outbox record đồng bộ trong cùng một transaction ở repo layer.
	Create(ctx context.Context, e *mailEntity.Endpoint, outbox *mailEntity.MailOutboxRecord) error
	GetByID(ctx context.Context, zoneID uuid.UUID, id uuid.UUID) (*mailEntity.Endpoint, error)
	GetGlobalByID(ctx context.Context, id uuid.UUID) (*mailEntity.Endpoint, error)
	ListByZone(ctx context.Context, zoneID uuid.UUID, cursor string, limit int) ([]*mailEntity.Endpoint, string, error)
	ListGlobal(ctx context.Context, cursor string, limit int) ([]*mailEntity.Endpoint, string, error)
	Update(ctx context.Context, e *mailEntity.Endpoint) error
	Delete(ctx context.Context, zoneID uuid.UUID, id uuid.UUID) error
}

