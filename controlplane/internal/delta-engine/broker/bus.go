package broker

import (
	"context"
	"controlplane/internal/delta-engine/types"
)

// ConfigEventBus định nghĩa giao thức trừu tượng cho hệ thống Message Bus (NATS JetStream).
// Việc sử dụng interface giúp tách rời hoàn toàn Broker cụ thể khỏi Core Delta Engine.
type ConfigEventBus interface {
	// Publish gửi sự kiện đồng bộ trạng thái đến Cluster.
	Publish(ctx context.Context, event types.DeltaEvent) error

	// Subscribe lắng nghe các sự kiện đồng bộ trạng thái từ các Node khác trong Cluster.
	Subscribe(ctx context.Context, handler func(types.DeltaEvent)) error

	// Close đóng kết nối của event bus một cách an toàn.
	Close() error
}
