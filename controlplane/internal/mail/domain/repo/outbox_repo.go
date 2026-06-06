package mailRepoInterface

import (
	"context"
	mailEntity "controlplane/internal/mail/domain/entity"
)

type MailOutboxRepository interface {
	Save(ctx context.Context, record *mailEntity.MailOutboxRecord) error
	FetchPendingForUpdate(ctx context.Context, limit int) ([]*mailEntity.MailOutboxRecord, error)
	MarkPublished(ctx context.Context, id int64) error
	MarkFailed(ctx context.Context, id int64, reason string) error
}
