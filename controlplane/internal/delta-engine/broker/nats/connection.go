package nats

import (
	"context"
	"encoding/json"
	"fmt"

	"controlplane/internal/delta-engine/broker"
	"controlplane/internal/delta-engine/types"
	"github.com/nats-io/nats.go"
)

// NatsEventBus triển khai interface ConfigEventBus, đóng vai trò là cầu nối truyền tin qua NATS.
type NatsEventBus struct {
	nc      *nats.Conn
	subject string
}

// NewNatsEventBus khởi tạo một NatsEventBus với kết nối nats.Conn được cấu hình sẵn.
func NewNatsEventBus(nc *nats.Conn, subject string) broker.ConfigEventBus {
	return &NatsEventBus{
		nc:      nc,
		subject: subject,
	}
}

func (b *NatsEventBus) Publish(ctx context.Context, event types.DeltaEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("nats_bus: failed to marshal event: %w", err)
	}

	return b.nc.Publish(b.subject, data)
}

func (b *NatsEventBus) Subscribe(ctx context.Context, handler func(types.DeltaEvent)) error {
	_, err := b.nc.Subscribe(b.subject, func(msg *nats.Msg) {
		var event types.DeltaEvent
		if err := json.Unmarshal(msg.Data, &event); err != nil {
			return
		}
		handler(event)
	})

	return err
}

func (b *NatsEventBus) Close() error {
	// Kết nối thô do infra quản lý vòng đời, ở đây chỉ ngắt các subscription nếu có.
	return nil
}
