package policySvcImpl

import (
	"context"
	"encoding/json"
	"strings"

	policyEntity "controlplane/internal/policyengine/domain/entity"

	goredis "github.com/redis/go-redis/v9"
)

type redisPubSubNotifier struct {
	client *goredis.Client
	topic  string
}

// NewRedisPubSubNotifier provisions notifier/subscriber cho kênh propagation nội bộ.
// CONTRACT: channel này chỉ chở metadata event, không chở full policy payload.
func NewRedisPubSubNotifier(client *goredis.Client, topic string) *redisPubSubNotifier {
	if strings.TrimSpace(topic) == "" {
		topic = "policyengine.policy.changed.v1"
	}
	return &redisPubSubNotifier{client: client, topic: topic}
}

// PublishPolicyChanged phát metadata event để instance khác trigger reload gần real-time.
// Fail publish không được rollback local state; caller sẽ fallback bằng poll loop.
func (n *redisPubSubNotifier) PublishPolicyChanged(ctx context.Context, event policyEntity.PolicyChangedEvent) error {
	if n == nil || n.client == nil {
		return nil
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return n.client.Publish(ctx, n.topic, payload).Err()
}

// SubscribePolicyChanged trả channel event consume cho worker runtime.
//
// Flow nội bộ theo từng bước:
// - B1: mở pub/sub subscription theo topic đã cấu hình.
// - B2: xác nhận subscribe bằng Receive(ctx) để fail-fast nếu Redis không sẵn sàng.
// - B3: bridge message Redis -> channel typed event bằng goroutine riêng.
// - B4: channel đóng sạch khi ctx cancel hoặc stream Redis đóng.
func (n *redisPubSubNotifier) SubscribePolicyChanged(ctx context.Context) (<-chan policyEntity.PolicyChangedEvent, error) {
	output := make(chan policyEntity.PolicyChangedEvent, 1)
	if n == nil || n.client == nil {
		// Case: notifier chưa được provision đúng.
		// Action: trả channel đóng ngay để worker upper layer degrade sang poll.
		close(output)
		return output, nil
	}
	subscriber := n.client.Subscribe(ctx, n.topic)
	if _, err := subscriber.Receive(ctx); err != nil {
		_ = subscriber.Close()
		close(output)
		return nil, err
	}
	messages := subscriber.Channel()
	go func() {
		defer close(output)
		defer subscriber.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case item, ok := <-messages:
				if !ok || item == nil {
					return
				}
				event := policyEntity.PolicyChangedEvent{}
				if err := json.Unmarshal([]byte(item.Payload), &event); err != nil {
					// Case: payload không parse được theo contract event.
					// Action: skip event lỗi để stream không bị block toàn cục.
					continue
				}
				output <- event
			}
		}
	}()
	return output, nil
}
