package coreCache

import (
	"context"
	"controlplane/pkg/logger"
	"encoding/json"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const SecretInvalidationChannel = "core:secret:invalidate"

type RuntimeSecretInvalidator interface {
	Invalidate(familyCode string)
}

type SecretInvalidationNotifier interface {
	InvalidateFamily(ctx context.Context, familyCode string, reason string) error
}

type SecretInvalidationEvent struct {
	FamilyCode string    `json:"family_code"`
	Reason     string    `json:"reason"`
	NodeID     string    `json:"node_id,omitempty"`
	At         time.Time `json:"at"`
}

type RedisSecretInvalidationBus struct {
	rdb      *goredis.Client
	provider RuntimeSecretInvalidator
	nodeID   string
}

func NewRedisSecretInvalidationBus(rdb *goredis.Client, provider RuntimeSecretInvalidator, nodeID string) *RedisSecretInvalidationBus {
	return &RedisSecretInvalidationBus{rdb: rdb, provider: provider, nodeID: strings.TrimSpace(nodeID)}
}

func (b *RedisSecretInvalidationBus) InvalidateFamily(ctx context.Context, familyCode string, reason string) error {
	familyCode = strings.TrimSpace(familyCode)
	if familyCode == "" {
		return nil
	}
	if b.provider != nil {
		b.provider.Invalidate(familyCode)
	}
	if b.rdb == nil {
		logger.SysInfoFields("core.secret.invalidate_publish", "invalidated secret cache locally without redis bus", logger.Fields{"family": familyCode, "reason": reason})
		return nil
	}
	event := SecretInvalidationEvent{FamilyCode: familyCode, Reason: strings.TrimSpace(reason), NodeID: b.nodeID, At: time.Now().UTC()}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	err = b.rdb.Publish(ctx, SecretInvalidationChannel, payload).Err()
	if err != nil {
		logger.SysWarnFields("core.secret.invalidate_publish", "failed to publish secret invalidation event", err, logger.Fields{"family": familyCode, "reason": reason})
		return err
	}
	logger.SysInfoFields("core.secret.invalidate_publish", "published secret invalidation event", logger.Fields{"family": familyCode, "reason": reason, "channel": SecretInvalidationChannel})
	return nil
}

func (b *RedisSecretInvalidationBus) Listen(ctx context.Context) error {
	logger.SysInfoFields("core.secret.invalidate_subscriber", "starting secret invalidation subscriber", logger.Fields{"channel": SecretInvalidationChannel})
	if b.rdb == nil || b.provider == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	pubsub := b.rdb.Subscribe(ctx, SecretInvalidationChannel)
	defer pubsub.Close()
	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-ch:
			if !ok {
				return nil
			}
			var event SecretInvalidationEvent
			if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
				logger.SysWarnFields("core.secret.invalidate_subscriber", "received malformed invalidation event", err, logger.Fields{"channel": SecretInvalidationChannel})
				continue
			}
			if strings.TrimSpace(event.FamilyCode) == "" {
				continue
			}
			b.provider.Invalidate(event.FamilyCode)
			logger.SysInfoFields("core.secret.invalidate_subscriber", "applied secret invalidation event", logger.Fields{"family": event.FamilyCode, "reason": event.Reason, "node_id": event.NodeID})
		}
	}
}

var _ SecretInvalidationNotifier = (*RedisSecretInvalidationBus)(nil)
