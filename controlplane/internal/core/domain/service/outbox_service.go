package coreSvcInterface

import (
	"context"
	coreEntity "controlplane/internal/core/domain/entity"
)

type OutboxService interface {
	// PublishEvent thêm một sự kiện mới vào bảng outbox dưới dạng hàng đợi giao dịch.
	PublishEvent(ctx context.Context, entity string, op string, payload []byte, version uint64) (*coreEntity.OutboxRecord, error)

	// ProcessPending quét các tin nhắn PENDING và phát chúng lên NATS Bus.
	ProcessPending(ctx context.Context, limit int) error
}
