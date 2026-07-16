// Package pubsub định nghĩa interface cho các NATS client (request-reply) của cost-manager.
// Đây là boundary giữa service layer và transport layer — service chỉ gọi qua interface này,
// không biết gì về nats.Conn hay protobuf bên dưới.
package pubsub

import (
	"context"

	"cost-manager/api/internal/domain/entity"
)

// ZoneNatsClient là interface cho NATS request-reply client lấy danh sách Zone từ Controlplane
type ZoneNatsClient interface {
	GetZoneList(ctx context.Context) ([]entity.Zone, error)
}
