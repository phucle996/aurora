package mailRepoInterface

import (
	"context"
	mailEntity "controlplane/internal/mail/domain/entity"
)

type MailOutboxRepository interface {
	// Create lưu trữ bản ghi Outbox mới vào database để CDC trích xuất phát tán
	Create(ctx context.Context, record *mailEntity.MailOutboxRecord) error
}
